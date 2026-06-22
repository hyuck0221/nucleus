package server

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shimhyuck/nucleus/internal/antigravity"
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
	ollama      *ollama.Client
	antigravity *antigravity.Client
	hf          *huggingface.Client
	config      *settings.Manager
	update      *updater.Client
	store       *store.Store
	logger      *slog.Logger

	imageJobsMu sync.RWMutex
	imageJobs   map[string]imageGenerationJob
}

func New(ollamaClient *ollama.Client, antigravityClient *antigravity.Client, usage *store.Store, logger *slog.Logger) *Server {
	srv := &Server{ollama: ollamaClient, antigravity: antigravityClient, hf: huggingface.New(), config: settings.New(), update: updater.New(), store: usage, logger: logger, imageJobs: make(map[string]imageGenerationJob)}
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
	mux.HandleFunc("/api/chat-models", s.chatModels)
	mux.HandleFunc("/api/image-models", s.imageModels)
	mux.HandleFunc("/api/model-suggestions", s.modelSuggestions)
	mux.HandleFunc("/api/models/pull", s.pull)
	mux.HandleFunc("/api/models/delete", s.deleteModel)
	mux.HandleFunc("/api/huggingface/models", s.huggingFaceModels)
	mux.HandleFunc("/api/settings", s.settings)
	mux.HandleFunc("/api/ollama/performance", s.ollamaPerformance)
	mux.HandleFunc("/api/ollama/preload", s.ollamaPreload)
	mux.HandleFunc("/api/ollama/stop", s.ollamaStop)
	mux.HandleFunc("/api/ollama/restart", s.ollamaRestart)
	mux.HandleFunc("/api/export/cli-config/options", s.cliConfigExportOptions)
	mux.HandleFunc("/api/export/cli-config/download", s.cliConfigExportDownload)
	mux.HandleFunc("/api/update/check", s.updateCheck)
	mux.HandleFunc("/api/update/download", s.updateDownload)
	mux.HandleFunc("/api/update/progress", s.updateProgress)
	mux.HandleFunc("/api/usage", s.usage)
	mux.HandleFunc("/api/usage/delete", s.deleteUsage)
	mux.HandleFunc("/api/usage/clear", s.clearUsage)
	mux.HandleFunc("/api/requests/stop", s.stopRequest)
	mux.HandleFunc("/api/images/generations", s.startImageGeneration)
	mux.HandleFunc("/api/images/generations/", s.imageGenerationJob)
	mux.HandleFunc("/api/events", s.events)
	mux.HandleFunc("/v1/models", s.openAIModels)
	mux.HandleFunc("/v1/chat/completions", s.chatCompletions)
	mux.HandleFunc("/v1/images/generations", s.imageGenerations)
	return requestLogger(corsMiddleware(mux), s.logger)
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
	ollamaStatusCh := make(chan ollama.Status, 1)
	antigravityStatusCh := make(chan antigravity.Status, 1)
	go func() {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		ollamaStatusCh <- s.ollama.Status(ctx)
	}()
	go func() {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		antigravityStatusCh <- s.antigravity.Status(ctx)
	}()
	active, recent := s.store.Snapshot()
	writeJSON(w, map[string]interface{}{
		"app":         map[string]string{"version": version.Version, "commit": version.Commit, "date": version.Date},
		"ollama":      <-ollamaStatusCh,
		"antigravity": <-antigravityStatusCh,
		"active":      active,
		"recent":      recent,
		"now":         time.Now(),
	})
}

func (s *Server) chatModels(w http.ResponseWriter, r *http.Request) {
	models, ollamaErr := s.ollama.Models(r.Context())
	data := make([]map[string]interface{}, 0, len(models)+1)
	for _, model := range models {
		data = append(data, map[string]interface{}{"name": model.Name, "provider": "ollama"})
	}
	if status := s.antigravity.Status(r.Context()); status.Installed {
		data = append(data, map[string]interface{}{"name": antigravity.ModelID, "provider": "antigravity-cli", "version": status.Version})
		if cliModels, err := s.antigravity.Models(r.Context()); err == nil {
			for _, model := range cliModels {
				data = append(data, map[string]interface{}{"name": antigravity.ModelID + "/" + model, "provider": "antigravity-cli", "version": status.Version})
			}
		}
	}
	if len(data) == 0 && ollamaErr != nil {
		http.Error(w, ollamaErr.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]interface{}{"models": data})
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

func (s *Server) imageModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.ollama.Models(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	imageModels := make([]ollama.Model, 0, len(models))
	for _, model := range models {
		if isImageGenerationModel(model.Name) {
			imageModels = append(imageModels, model)
		}
	}
	writeJSON(w, map[string]interface{}{"models": imageModels})
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

func (s *Server) ollamaPerformance(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.writeOllamaPerformance(w, "")
	case http.MethodPost:
		var req settings.OllamaPerformance
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		limits := performanceLimits()
		next := clampOllamaPerformance(req, limits)
		cfg := s.config.Get()
		cfg.OllamaPerformance = next
		if err := s.config.Save(cfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := applyOllamaEnvironment(next); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		s.writeOllamaPerformance(w, "Applied. Restart Ollama to use the new runtime limits.")
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) ollamaPreload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	model, ok := modelFromJSONRequest(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.ollama.Preload(ctx, model); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "model": model, "action": "preload"})
}

func (s *Server) ollamaStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	model, ok := modelFromJSONRequest(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.ollama.Stop(ctx, model); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "model": model, "action": "stop"})
}

func (s *Server) ollamaRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if runtime.GOOS != "darwin" {
		http.Error(w, "restart is only available on macOS", http.StatusBadRequest)
		return
	}
	_ = exec.Command("osascript", "-e", `tell application "Ollama" to quit`).Run()
	time.Sleep(800 * time.Millisecond)
	if err := exec.Command("open", "-a", "Ollama").Start(); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "action": "restart"})
}

func modelFromJSONRequest(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req struct {
		Model string `json:"model"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", false
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = strings.TrimSpace(req.Name)
	}
	if model == "" {
		http.Error(w, "expected JSON body: {\"model\":\"llama3.2\"}", http.StatusBadRequest)
		return "", false
	}
	return model, true
}

func (s *Server) writeOllamaPerformance(w http.ResponseWriter, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	running, err := s.ollama.RunningModels(ctx)
	errorText := ""
	if err != nil {
		errorText = err.Error()
	}
	writeJSON(w, map[string]interface{}{
		"settings":          clampOllamaPerformance(s.config.Get().OllamaPerformance, performanceLimits()),
		"applied":           currentOllamaEnvironment(),
		"limits":            performanceLimits(),
		"runningModels":     running,
		"runningModelError": errorText,
		"restartRequired":   true,
		"message":           message,
	})
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

func (s *Server) cliConfigExportDownload(w http.ResponseWriter, r *http.Request) {
	tool := strings.TrimSpace(r.URL.Query().Get("tool"))
	baseURL := strings.TrimSpace(r.URL.Query().Get("base_url"))
	contextSize := strings.TrimSpace(r.URL.Query().Get("context_size"))
	if baseURL == "" {
		http.Error(w, "base_url is required", http.StatusBadRequest)
		return
	}
	models, err := s.ollama.Models(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	modelNames := make([]string, 0, len(models))
	for _, model := range models {
		modelNames = append(modelNames, model.Name)
	}
	if len(modelNames) == 0 {
		http.Error(w, "no installed models available", http.StatusBadRequest)
		return
	}
	size := exportContextSize(contextSize)
	filename := "opencode.json"
	var payload interface{}
	switch tool {
	case "openclaw":
		filename = "models.json"
		payload = buildOpenClawExport(baseURL, modelNames, size)
	default:
		payload = buildOpenCodeExport(baseURL, modelNames, size)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(payload)
}

func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
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

func (s *Server) startImageGeneration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	job := s.startImageGenerationJob(r, body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, job)
}

func (s *Server) imageGenerationJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/images/generations/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	s.imageJobsMu.RLock()
	job, ok := s.imageJobs[id]
	s.imageJobsMu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, job)
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
	antigravityStatus := s.antigravity.Status(r.Context())
	if err != nil && !antigravityStatus.Installed {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	data := make([]map[string]interface{}, 0, len(models)+1)
	for _, model := range models {
		data = append(data, map[string]interface{}{"id": model.Name, "object": "model", "owned_by": "local-ollama"})
	}
	if antigravityStatus.Installed {
		data = append(data, map[string]interface{}{"id": antigravity.ModelID, "object": "model", "owned_by": "local-antigravity-cli"})
		if cliModels, modelsErr := s.antigravity.Models(r.Context()); modelsErr == nil {
			for _, model := range cliModels {
				data = append(data, map[string]interface{}{"id": antigravity.ModelID + "/" + model, "object": "model", "owned_by": "local-antigravity-cli"})
			}
		}
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
	if antigravity.IsModel(model) {
		s.chatCompletionsAntigravity(w, r, body, model)
		return
	}
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
	copyErr := copyAndFlush(w, resp.Body)
	errText := ""
	if copyErr != nil {
		errText = copyErr.Error()
	}
	s.store.Finish(id, resp.StatusCode, errText)
}

type antigravityChatRequest struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
	Tools      json.RawMessage `json:"tools"`
	ToolChoice json.RawMessage `json:"tool_choice"`
	Functions  json.RawMessage `json:"functions"`
}

func (s *Server) chatCompletionsAntigravity(w http.ResponseWriter, r *http.Request, body []byte, model string) {
	if status := s.antigravity.Status(r.Context()); !status.Installed {
		http.Error(w, "Antigravity CLI is not installed or is not available on the Nucleus PATH", http.StatusServiceUnavailable)
		return
	}
	var req antigravityChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if hasJSONValue(req.Tools) || hasJSONValue(req.ToolChoice) || hasJSONValue(req.Functions) {
		http.Error(w, "Antigravity CLI API models support text chat only; tools, functions, and tool_choice are not allowed", http.StatusBadRequest)
		return
	}
	messages := make([]antigravity.Message, 0, len(req.Messages))
	for _, message := range req.Messages {
		content, err := textContent(message.Content)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		role := strings.TrimSpace(message.Role)
		if role != "system" && role != "user" && role != "assistant" && role != "developer" {
			http.Error(w, "Antigravity CLI API models only accept system, developer, user, and assistant messages", http.StatusBadRequest)
			return
		}
		messages = append(messages, antigravity.Message{Role: role, Content: content})
	}
	prompt, err := antigravity.BuildPrompt(messages)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	id := randomID()
	created := time.Now()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	s.store.Start(store.RequestRecord{
		ID: id, User: userFromRequest(r), Client: clientFromRequest(r), Model: model,
		Path: r.URL.Path, StartedAt: created,
	}, cancel)

	statusCode := http.StatusOK
	errText := ""
	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)
		writeAntigravityChunk(w, flusher, id, model, created, map[string]interface{}{"role": "assistant"}, nil)
		_, err = s.antigravity.Complete(ctx, model, prompt, func(delta string) error {
			return writeAntigravityChunk(w, flusher, id, model, created, map[string]interface{}{"content": delta}, nil)
		})
		if err != nil {
			statusCode = http.StatusBadGateway
			errText = err.Error()
			payload, _ := json.Marshal(map[string]interface{}{"error": map[string]interface{}{"message": errText, "type": "antigravity_cli_error"}})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
		} else {
			finish := "stop"
			writeAntigravityChunk(w, flusher, id, model, created, map[string]interface{}{}, &finish)
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	} else {
		var result antigravity.Result
		result, err = s.antigravity.Complete(ctx, model, prompt, nil)
		if err != nil {
			statusCode = http.StatusBadGateway
			errText = err.Error()
			http.Error(w, errText, statusCode)
		} else {
			writeJSON(w, map[string]interface{}{
				"id": id, "object": "chat.completion", "created": created.Unix(), "model": result.Model,
				"choices": []interface{}{map[string]interface{}{
					"index": 0, "message": map[string]interface{}{"role": "assistant", "content": result.Content}, "finish_reason": "stop",
				}},
				"usage": map[string]int{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
			})
		}
	}
	s.store.Finish(id, statusCode, errText)
}

func writeAntigravityChunk(w io.Writer, flusher http.Flusher, id, model string, created time.Time, delta map[string]interface{}, finishReason *string) error {
	payload, err := json.Marshal(map[string]interface{}{
		"id": id, "object": "chat.completion.chunk", "created": created.Unix(), "model": model,
		"choices": []interface{}{map[string]interface{}{"index": 0, "delta": delta, "finish_reason": finishReason}},
	})
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return err
	}
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}

func hasJSONValue(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null" && trimmed != "[]"
}

func textContent(raw json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", errors.New("Antigravity CLI API models require string or text-only message content")
	}
	var joined strings.Builder
	for _, part := range parts {
		if part.Type != "text" && part.Type != "input_text" {
			return "", errors.New("Antigravity CLI API models do not accept images, files, or other non-text content")
		}
		joined.WriteString(part.Text)
	}
	return joined.String(), nil
}

func (s *Server) imageGenerations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if wantsAsyncImageGeneration(r) {
		job := s.startImageGenerationJob(r, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, job)
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
	resp, err := s.ollama.ProxyOpenAIImages(ctx, strings.NewReader(string(body)))
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
	copyErr := copyAndFlush(w, resp.Body)
	errText := ""
	if copyErr != nil {
		errText = copyErr.Error()
	}
	s.store.Finish(id, resp.StatusCode, errText)
}

type imageGenerationJob struct {
	ID         string          `json:"id"`
	Status     string          `json:"status"`
	Model      string          `json:"model"`
	Error      string          `json:"error,omitempty"`
	StatusCode int             `json:"statusCode,omitempty"`
	Response   json.RawMessage `json:"response,omitempty"`
	URL        string          `json:"url"`
	StartedAt  time.Time       `json:"startedAt"`
	FinishedAt *time.Time      `json:"finishedAt,omitempty"`
}

func (s *Server) startImageGenerationJob(r *http.Request, body []byte) imageGenerationJob {
	id := randomID()
	model := modelFromBody(body)
	job := imageGenerationJob{
		ID:        id,
		Status:    "running",
		Model:     model,
		URL:       "/api/images/generations/" + id,
		StartedAt: time.Now(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	record := store.RequestRecord{
		ID:        id,
		User:      userFromRequest(r),
		Client:    clientFromRequest(r),
		Model:     model,
		Path:      "/v1/images/generations",
		StartedAt: job.StartedAt,
	}
	s.imageJobsMu.Lock()
	s.imageJobs[id] = job
	s.imageJobsMu.Unlock()
	s.store.Start(record, cancel)
	go s.runImageGenerationJob(ctx, cancel, id, body)
	return job
}

func (s *Server) runImageGenerationJob(ctx context.Context, cancel context.CancelFunc, id string, body []byte) {
	defer cancel()
	resp, err := s.ollama.ProxyOpenAIImages(ctx, strings.NewReader(string(body)))
	statusCode := http.StatusOK
	errText := ""
	var payload []byte
	if err != nil {
		statusCode = http.StatusBadGateway
		errText = err.Error()
	} else {
		defer resp.Body.Close()
		statusCode = resp.StatusCode
		payload, err = io.ReadAll(resp.Body)
		if err != nil {
			errText = err.Error()
			if statusCode < 400 {
				statusCode = http.StatusBadGateway
			}
		}
		if statusCode >= 400 && errText == "" {
			errText = strings.TrimSpace(string(payload))
			if errText == "" {
				errText = resp.Status
			}
		}
	}

	s.imageJobsMu.Lock()
	job := s.imageJobs[id]
	job.StatusCode = statusCode
	finishedAt := time.Now()
	job.FinishedAt = &finishedAt
	if errText != "" || statusCode >= 400 {
		job.Status = "error"
		job.Error = errText
	} else {
		job.Status = "done"
		job.Response = json.RawMessage(payload)
	}
	s.imageJobs[id] = job
	s.imageJobsMu.Unlock()
	s.store.Finish(id, statusCode, errText)
}

func wantsAsyncImageGeneration(r *http.Request) bool {
	if strings.EqualFold(r.URL.Query().Get("async"), "true") || r.URL.Query().Get("async") == "1" {
		return true
	}
	return strings.Contains(strings.ToLower(r.Header.Get("Prefer")), "respond-async")
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

func copyAndFlush(w http.ResponseWriter, r io.Reader) error {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

type ollamaPerformanceLimits struct {
	NumParallelMax     int   `json:"numParallelMax"`
	MaxLoadedModelsMax int   `json:"maxLoadedModelsMax"`
	MaxQueueMax        int   `json:"maxQueueMax"`
	ContextLengthMax   int   `json:"contextLengthMax"`
	CPUThreadsMax      int   `json:"cpuThreadsMax"`
	LogicalCPU         int   `json:"logicalCpu"`
	MemoryBytes        int64 `json:"memoryBytes"`
}

func performanceLimits() ollamaPerformanceLimits {
	mem := systemMemoryBytes()
	memGB := int(mem / (1024 * 1024 * 1024))
	if memGB <= 0 {
		memGB = 8
	}
	cpus := runtime.NumCPU()
	if cpus <= 0 {
		cpus = 1
	}
	maxLoaded := memGB / 4
	if maxLoaded < 1 {
		maxLoaded = 1
	}
	if maxLoaded > 8 {
		maxLoaded = 8
	}
	contextMax := 4096 * memGB
	if contextMax < 4096 {
		contextMax = 4096
	}
	if contextMax > 131072 {
		contextMax = 131072
	}
	return ollamaPerformanceLimits{
		NumParallelMax:     cpus,
		MaxLoadedModelsMax: maxLoaded,
		MaxQueueMax:        2048,
		ContextLengthMax:   contextMax,
		CPUThreadsMax:      cpus,
		LogicalCPU:         cpus,
		MemoryBytes:        mem,
	}
}

func clampOllamaPerformance(cfg settings.OllamaPerformance, limits ollamaPerformanceLimits) settings.OllamaPerformance {
	cfg.NumParallel = clampInt(cfg.NumParallel, 1, limits.NumParallelMax)
	cfg.MaxLoadedModels = clampInt(cfg.MaxLoadedModels, 1, limits.MaxLoadedModelsMax)
	cfg.MaxQueue = clampInt(cfg.MaxQueue, 1, limits.MaxQueueMax)
	cfg.ContextLength = clampInt(cfg.ContextLength, 1024, limits.ContextLengthMax)
	cfg.CPUThreads = clampInt(cfg.CPUThreads, 1, limits.CPUThreadsMax)
	cfg.ContextLength = (cfg.ContextLength / 1024) * 1024
	if cfg.ContextLength < 1024 {
		cfg.ContextLength = 1024
	}
	cfg.KeepAlive = strings.TrimSpace(cfg.KeepAlive)
	if cfg.KeepAlive == "" {
		cfg.KeepAlive = "5m"
	}
	switch cfg.KVCacheType {
	case "f16", "q8_0", "q4_0":
	default:
		cfg.KVCacheType = "f16"
	}
	return cfg
}

func clampInt(value, min, max int) int {
	if max < min {
		max = min
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func systemMemoryBytes() int64 {
	if runtime.GOOS != "darwin" {
		return 0
	}
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func applyOllamaEnvironment(cfg settings.OllamaPerformance) error {
	values := map[string]string{
		"OLLAMA_NUM_PARALLEL":      strconv.Itoa(cfg.NumParallel),
		"OLLAMA_MAX_LOADED_MODELS": strconv.Itoa(cfg.MaxLoadedModels),
		"OLLAMA_MAX_QUEUE":         strconv.Itoa(cfg.MaxQueue),
		"OLLAMA_CONTEXT_LENGTH":    strconv.Itoa(cfg.ContextLength),
		"OLLAMA_NUM_THREADS":       strconv.Itoa(cfg.CPUThreads),
		"OLLAMA_KEEP_ALIVE":        cfg.KeepAlive,
		"OLLAMA_FLASH_ATTENTION":   boolEnv(cfg.FlashAttention),
		"OLLAMA_KV_CACHE_TYPE":     cfg.KVCacheType,
	}
	for key, value := range values {
		if runtime.GOOS == "darwin" {
			if err := exec.Command("launchctl", "setenv", key, value).Run(); err != nil {
				return fmt.Errorf("launchctl setenv %s failed: %w", key, err)
			}
		}
		_ = os.Setenv(key, value)
	}
	return nil
}

func currentOllamaEnvironment() map[string]string {
	keys := []string{"OLLAMA_NUM_PARALLEL", "OLLAMA_MAX_LOADED_MODELS", "OLLAMA_MAX_QUEUE", "OLLAMA_CONTEXT_LENGTH", "OLLAMA_NUM_THREADS", "OLLAMA_KEEP_ALIVE", "OLLAMA_FLASH_ATTENTION", "OLLAMA_KV_CACHE_TYPE"}
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		value := os.Getenv(key)
		if runtime.GOOS == "darwin" {
			if out, err := exec.Command("launchctl", "getenv", key).Output(); err == nil && strings.TrimSpace(string(out)) != "" {
				value = strings.TrimSpace(string(out))
			}
		}
		values[key] = value
	}
	return values
}

func boolEnv(value bool) string {
	if value {
		return "1"
	}
	return "0"
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
	output, err := tailscaleStatusJSON(ctx)
	if err != nil {
		return "", false
	}
	var status struct {
		Self struct {
			DNSName      string   `json:"DNSName"`
			TailscaleIPs []string `json:"TailscaleIPs"`
		} `json:"Self"`
	}
	if err := json.Unmarshal(output, &status); err != nil {
		return "", false
	}
	host := strings.TrimSuffix(strings.TrimSpace(status.Self.DNSName), ".")
	if host != "" {
		return host, true
	}
	for _, ip := range status.Self.TailscaleIPs {
		if parsed := net.ParseIP(strings.TrimSpace(ip)); parsed != nil && parsed.To4() != nil {
			return parsed.String(), true
		}
	}
	return "", false
}

func tailscaleStatusJSON(ctx context.Context) ([]byte, error) {
	candidates := [][]string{
		{"tailscale", "status", "--json"},
		{"/Applications/Tailscale.app/Contents/MacOS/Tailscale", "status", "--json"},
	}
	var lastErr error
	for _, args := range candidates {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Env = append(os.Environ(), "TAILSCALE_BE_CLI=1")
		output, err := cmd.Output()
		if err == nil {
			return output, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func exportContextSize(value string) int {
	const fallback = 32768
	size, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || size < 4096 {
		return fallback
	}
	if size > 131072 {
		return 131072
	}
	return size
}

func buildOpenCodeExport(baseURL string, models []string, contextSize int) map[string]interface{} {
	modelMap := make(map[string]interface{}, len(models))
	for _, model := range models {
		modelMap[model] = map[string]interface{}{
			"name": model,
			"options": map[string]interface{}{
				"num_ctx": contextSize,
			},
		}
	}
	return map[string]interface{}{
		"$schema": "https://opencode.ai/config.json",
		"provider": map[string]interface{}{
			"nucleus": map[string]interface{}{
				"npm":  "@ai-sdk/openai-compatible",
				"name": "Nucleus",
				"options": map[string]interface{}{
					"baseURL": baseURL,
				},
				"models": modelMap,
			},
		},
	}
}

func buildOpenClawExport(baseURL string, models []string, contextSize int) map[string]interface{} {
	modelList := make([]map[string]interface{}, 0, len(models))
	for _, model := range models {
		modelList = append(modelList, map[string]interface{}{
			"id":            model,
			"name":          model,
			"reasoning":     false,
			"input":         []string{"text"},
			"contextWindow": contextSize,
			"contextTokens": contextSize,
		})
	}
	return map[string]interface{}{
		"models": map[string]interface{}{
			"mode": "merge",
			"providers": map[string]interface{}{
				"nucleus": map[string]interface{}{
					"baseUrl": baseURL,
					"api":     "openai-completions",
					"models":  modelList,
				},
			},
		},
	}
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

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := w.Header()
		headers.Set("Access-Control-Allow-Origin", "*")
		headers.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		headers.Set("Access-Control-Allow-Headers", corsAllowedHeaders(r))
		headers.Set("Access-Control-Expose-Headers", "Content-Type, Content-Length")
		headers.Set("Access-Control-Max-Age", "86400")
		headers.Set("Access-Control-Allow-Private-Network", "true")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func corsAllowedHeaders(r *http.Request) string {
	if requested := strings.TrimSpace(r.Header.Get("Access-Control-Request-Headers")); requested != "" {
		return requested
	}
	return "Authorization, Content-Type, Accept, Origin, X-Requested-With, X-Nucleus-User, X-Nucleus-Client, X-User, X-Forwarded-User, Prefer"
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
	{Name: "x/z-image-turbo", Description: "Image generation model"},
	{Name: "x/flux2-klein", Description: "Image generation model"},
}

func isImageGenerationModel(name string) bool {
	value := strings.ToLower(strings.TrimSpace(name))
	if value == "" {
		return false
	}
	if value == "x/z-image-turbo" || strings.HasPrefix(value, "x/z-image-turbo:") {
		return true
	}
	if value == "x/flux2-klein" || strings.HasPrefix(value, "x/flux2-klein:") {
		return true
	}
	imageHints := []string{"z-image", "flux2-klein", "text-to-image", "image-generation"}
	for _, hint := range imageHints {
		if strings.Contains(value, hint) {
			return true
		}
	}
	return false
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
