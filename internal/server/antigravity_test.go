package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shimhyuck/nucleus/internal/antigravity"
	"github.com/shimhyuck/nucleus/internal/ollama"
	"github.com/shimhyuck/nucleus/internal/store"
)

func TestAntigravityChatCompletion(t *testing.T) {
	handler := antigravityTestHandler(t, fakeAntigravityCommand(t))
	body := `{"model":"antigravity-cli","messages":[{"role":"user","content":"hello"}]}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Choices) != 1 || response.Choices[0].Message.Content != "from cli" {
		t.Fatalf("unexpected response: %s", recorder.Body.String())
	}
}

func TestAntigravityChatRejectsTools(t *testing.T) {
	handler := antigravityTestHandler(t, fakeAntigravityCommand(t))
	body := `{"model":"antigravity-cli","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function"}]}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestAntigravityChatStreamsOpenAIChunks(t *testing.T) {
	handler := antigravityTestHandler(t, fakeAntigravityCommand(t))
	body := `{"model":"antigravity-cli","stream":true,"messages":[{"role":"user","content":"hello"}]}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"content":"from cli\n"`) || !strings.Contains(recorder.Body.String(), "data: [DONE]") {
		t.Fatalf("unexpected stream response %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestAntigravityChatRequiresInstalledCLI(t *testing.T) {
	handler := antigravityTestHandler(t, filepath.Join(t.TempDir(), "missing-agy"))
	modelsRecorder := httptest.NewRecorder()
	handler.ServeHTTP(modelsRecorder, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if strings.Contains(modelsRecorder.Body.String(), antigravity.ModelID) {
		t.Fatalf("missing Antigravity CLI was exposed in model list: %s", modelsRecorder.Body.String())
	}

	body := `{"model":"antigravity-cli","messages":[{"role":"user","content":"hello"}]}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "not installed") {
		t.Fatalf("expected missing CLI error, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func antigravityTestHandler(t *testing.T, command string) http.Handler {
	t.Helper()
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"models":[]}`)
	}))
	t.Cleanup(ollamaServer.Close)
	return New(ollama.New(ollamaServer.URL), antigravity.New(command), store.New(10), slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
}

func fakeAntigravityCommand(t *testing.T) string {
	t.Helper()
	command := filepath.Join(t.TempDir(), "agy")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo 1.0.10
  exit 0
fi
if [ "$1" = "models" ]; then
  echo 'Gemini 3.5 Flash (High)'
  exit 0
fi
printf 'from cli\n'
`
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return command
}
