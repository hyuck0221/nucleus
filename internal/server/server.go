package server

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/shimhyuck/nucleus/internal/huggingface"
	"github.com/shimhyuck/nucleus/internal/ollama"
	"github.com/shimhyuck/nucleus/internal/settings"
	"github.com/shimhyuck/nucleus/internal/store"
	"github.com/shimhyuck/nucleus/internal/updater"
	"github.com/shimhyuck/nucleus/internal/version"
)

//go:embed web/*
var webFS embed.FS

type Server struct {
	ollama *ollama.Client
	hf     *huggingface.Client
	config *settings.Manager
	update *updater.Client
	store  *store.Store
	logger *slog.Logger
}

func New(ollamaClient *ollama.Client, usage *store.Store, logger *slog.Logger) *Server {
	srv := &Server{ollama: ollamaClient, hf: huggingface.New(), config: settings.New(), update: updater.New(), store: usage, logger: logger}
	go srv.cleanupUsageLoop()
	return srv
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	webRoot, _ := fs.Sub(webFS, "web")
	mux.HandleFunc("/", s.dashboard)
	mux.Handle("/assets/", http.FileServer(http.FS(webRoot)))
	mux.HandleFunc("/favicon.ico", s.favicon)
	mux.HandleFunc("/api/status", s.status)
	mux.HandleFunc("/api/models", s.models)
	mux.HandleFunc("/api/model-suggestions", s.modelSuggestions)
	mux.HandleFunc("/api/models/pull", s.pull)
	mux.HandleFunc("/api/models/delete", s.deleteModel)
	mux.HandleFunc("/api/huggingface/models", s.huggingFaceModels)
	mux.HandleFunc("/api/settings", s.settings)
	mux.HandleFunc("/api/export/cli-config/options", s.cliConfigExportOptions)
	mux.HandleFunc("/api/update/check", s.updateCheck)
	mux.HandleFunc("/api/update/download", s.updateDownload)
	mux.HandleFunc("/api/update/progress", s.updateProgress)
	mux.HandleFunc("/api/usage", s.usage)
	mux.HandleFunc("/api/usage/delete", s.deleteUsage)
	mux.HandleFunc("/api/usage/clear", s.clearUsage)
	mux.HandleFunc("/api/requests/stop", s.stopRequest)
	mux.HandleFunc("/api/events", s.events)
	mux.HandleFunc("/v1/models", s.openAIModels)
	mux.HandleFunc("/v1/chat/completions", s.chatCompletions)
	return requestLogger(mux, s.logger)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := webFS.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) favicon(w http.ResponseWriter, r *http.Request) {
	data, err := webFS.ReadFile("web/assets/icons/favicon-32.png")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(data)
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	active, recent := s.store.Snapshot()
	writeJSON(w, map[string]interface{}{
		"app":    map[string]string{"version": version.Version, "commit": version.Commit, "date": version.Date},
		"ollama": s.ollama.Status(ctx),
		"active": active,
		"recent": recent,
		"now":    time.Now(),
	})
}

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	models, err := s.ollama.Models(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]interface{}{"models": models})
}

func (s *Server) modelSuggestions(w http.ResponseWriter, r *http.Request) {
	models, _ := s.ollama.Models(r.Context())
	writeJSON(w, map[string]interface{}{
		"suggestions": suggestModels(r.URL.Query().Get("q"), models, 10),
	})
}

func (s *Server) pull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		http.Error(w, "expected JSON body: {\"name\":\"llama3.2\"}", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	flusher, _ := w.(http.Flusher)
	err := s.ollama.Pull(r.Context(), req.Name, func(line []byte) {
		s.store.Publish("model_pull", json.RawMessage(line))
		_, _ = w.Write(append(line, '\n'))
		if flusher != nil {
			flusher.Flush()
		}
	})
	if err != nil {
		s.store.Publish("error", err.Error())
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if cleanName, ok := cleanHuggingFaceModelName(req.Name); ok {
		if err := s.ollama.Copy(r.Context(), req.Name, cleanName); err != nil {
			s.store.Publish("error", err.Error())
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if err := s.ollama.Delete(r.Context(), req.Name); err != nil {
			s.store.Publish("error", err.Error())
		}
		s.store.Publish("model_renamed", map[string]string{"source": req.Name, "name": cleanName})
	}
}

func (s *Server) deleteModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		http.Error(w, "expected JSON body: {\"name\":\"llama3.2\"}", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	if err := s.ollama.Delete(r.Context(), name); err != nil {
		s.store.Publish("error", err.Error())
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	s.store.Publish("model_deleted", map[string]string{"name": name})
	writeJSON(w, map[string]interface{}{"deleted": true, "name": name})
}

func (s *Server) huggingFaceModels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	models, err := s.hf.SearchModels(ctx, r.URL.Query().Get("q"), 20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]interface{}{"models": models})
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.config.Get())
	case http.MethodPost:
		var cfg settings.Settings
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.config.Save(cfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, s.config.Get())
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) cliConfigExportOptions(w http.ResponseWriter, r *http.Request) {
	models, err := s.ollama.Models(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	modelNames := make([]string, 0, len(models))
	for _, model := range models {
		modelNames = append(modelNames, model.Name)
	}
	port := requestPort(r)
	localHost := firstPrivateIPv4()
	localBaseURL := ""
	if localHost != "" {
		localBaseURL = fmt.Sprintf("http://%s:%s/v1", localHost, port)
	}
	tailscaleHost, tailscaleOK := tailscaleDNSName(r.Context())
	tailscaleBaseURL := ""
	if tailscaleOK {
		tailscaleBaseURL = fmt.Sprintf("http://%s:%s/v1", tailscaleHost, port)
	}
	writeJSON(w, map[string]interface{}{
		"models": modelNames,
		"addresses": map[string]interface{}{
			"local": map[string]interface{}{
				"available": localBaseURL != "",
				"baseURL":   localBaseURL,
			},
			"tailscale": map[string]interface{}{
				"available": tailscaleOK,
				"baseURL":   tailscaleBaseURL,
			},
		},
	})
}

func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	writeJSON(w, s.update.Check(ctx, version.Version))
}

func (s *Server) updateDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	writeJSON(w, s.update.StartDownload(ctx, version.Version))
}

func (s *Server) updateProgress(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.update.State())
}

func (s *Server) usage(w http.ResponseWriter, r *http.Request) {
	active, recent := s.store.Snapshot()
	writeJSON(w, map[string]interface{}{"active": active, "recent": recent})
}

func (s *Server) deleteUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.ID) == "" {
		http.Error(w, "expected id", http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]bool{"deleted": s.store.DeleteRecent(req.ID)})
}

func (s *Server) clearUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.store.ClearRecent()
	writeJSON(w, map[string]bool{"cleared": true})
}

func (s *Server) stopRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.ID) == "" {
		http.Error(w, "expected id", http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]bool{"stopped": s.store.Stop(req.ID)})
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	events, cancel := s.store.Subscribe()
	defer cancel()
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-events:
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, event.JSON())
			flusher.Flush()
		case <-time.After(20 * time.Second):
			_, _ = w.Write([]byte(": heartbeat\n\n"))
			flusher.Flush()
		}
	}
}

func (s *Server) openAIModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.ollama.Models(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	data := make([]map[string]interface{}, 0, len(models))
	for _, model := range models {
		data = append(data, map[string]interface{}{"id": model.Name, "object": "model", "owned_by": "local-ollama"})
	}
	writeJSON(w, map[string]interface{}{"object": "list", "data": data})
}

func (s *Server) chatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	model := modelFromBody(body)
	id := randomID()
	record := store.RequestRecord{
		ID:        id,
		User:      userFromRequest(r),
		Client:    clientFromRequest(r),
		Model:     model,
		Path:      r.URL.Path,
		StartedAt: time.Now(),
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	s.store.Start(record, cancel)
	resp, err := s.ollama.ProxyOpenAI(ctx, strings.NewReader(string(body)))
	if err != nil {
		s.store.Finish(id, http.StatusBadGateway, err.Error())
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, copyErr := io.Copy(w, resp.Body)
	errText := ""
	if copyErr != nil {
		errText = copyErr.Error()
	}
	s.store.Finish(id, resp.StatusCode, errText)
}

func (s *Server) cleanupUsageLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cfg := s.config.Get()
		if !cfg.AutoDeleteUsage || cfg.UsageRetentionHours <= 0 {
			continue
		}
		removed := s.store.ClearOlderThan(time.Now().Add(-time.Duration(cfg.UsageRetentionHours) * time.Hour))
		if removed > 0 {
			s.store.Publish("usage_auto_deleted", map[string]int{"count": removed})
		}
	}
}

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}

func requestPort(r *http.Request) string {
	_, port, err := net.SplitHostPort(r.Host)
	if err == nil && strings.TrimSpace(port) != "" {
		return port
	}
	if strings.HasSuffix(r.Host, "]") {
		return "8787"
	}
	return "8787"
}

func firstPrivateIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		network, ok := addr.(*net.IPNet)
		if !ok || network.IP == nil || network.IP.IsLoopback() {
			continue
		}
		ip := network.IP.To4()
		if ip == nil || !ip.IsPrivate() {
			continue
		}
		return ip.String()
	}
	return ""
}

func tailscaleDNSName(ctx context.Context) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "tailscale", "status", "--json").Output()
	if err != nil {
		return "", false
	}
	var status struct {
		Self struct {
			DNSName string `json:"DNSName"`
		} `json:"Self"`
	}
	if err := json.Unmarshal(output, &status); err != nil {
		return "", false
	}
	host := strings.TrimSuffix(strings.TrimSpace(status.Self.DNSName), ".")
	return host, host != ""
}

func modelFromBody(body []byte) string {
	var req struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &req)
	if req.Model == "" {
		return "unknown"
	}
	return req.Model
}

func userFromRequest(r *http.Request) string {
	for _, h := range []string{"X-Nucleus-User", "X-User", "X-Forwarded-User"} {
		if value := strings.TrimSpace(r.Header.Get(h)); value != "" {
			return value
		}
	}
	if r.Header.Get("Authorization") != "" {
		return "api-client"
	}
	return "anonymous"
}

func clientFromRequest(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-Nucleus-Client")); value != "" {
		return value
	}
	if value := strings.TrimSpace(r.UserAgent()); value != "" {
		return value
	}
	return r.RemoteAddr
}

func randomID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func requestLogger(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
	})
}

type modelSuggestion struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Installed   bool   `json:"installed"`
}

var catalogModels = []modelSuggestion{
	{Name: "llama3.2", Description: "Meta general-purpose model"},
	{Name: "llama3.1", Description: "Meta general-purpose model"},
	{Name: "llama3", Description: "Meta general-purpose model"},
	{Name: "qwen2.5-coder", Description: "Code-focused model"},
	{Name: "qwen2.5", Description: "Multilingual general-purpose model"},
	{Name: "mistral", Description: "Fast general-purpose model"},
	{Name: "mixtral", Description: "Mixture-of-experts model"},
	{Name: "gemma2", Description: "Google general-purpose model"},
	{Name: "phi4", Description: "Compact reasoning model"},
	{Name: "phi3", Description: "Compact general-purpose model"},
	{Name: "deepseek-r1", Description: "Reasoning model"},
	{Name: "codellama", Description: "Code generation model"},
	{Name: "nomic-embed-text", Description: "Embedding model"},
	{Name: "llava", Description: "Vision-language model"},
}

func suggestModels(query string, installed []ollama.Model, limit int) []modelSuggestion {
	q := strings.ToLower(strings.TrimSpace(query))
	seen := make(map[string]struct{})
	suggestions := make([]modelSuggestion, 0, limit)
	add := func(item modelSuggestion) {
		if len(suggestions) >= limit {
			return
		}
		key := strings.ToLower(item.Name)
		if _, ok := seen[key]; ok {
			return
		}
		if q != "" && !strings.Contains(key, q) && !strings.Contains(strings.ToLower(item.Description), q) {
			return
		}
		seen[key] = struct{}{}
		suggestions = append(suggestions, item)
	}
	for _, model := range installed {
		add(modelSuggestion{Name: model.Name, Description: "Installed locally", Installed: true})
	}
	for _, model := range catalogModels {
		add(model)
	}
	return suggestions
}

func cleanHuggingFaceModelName(name string) (string, bool) {
	trimmed := strings.TrimSpace(name)
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "hf.co/") {
		return "", false
	}
	withoutPrefix := strings.TrimPrefix(trimmed, "hf.co/")
	parts := strings.Split(withoutPrefix, "/")
	if len(parts) < 2 {
		return "", false
	}
	model := parts[len(parts)-1]
	if model == "" {
		return "", false
	}
	return model, model != trimmed
}
