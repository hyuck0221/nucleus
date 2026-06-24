package server

import (
	"bytes"
	"encoding/base64"
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
	body := `{"model":"gemini-3.5-flash-high","messages":[{"role":"user","content":"hello"}]}`
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
	body := `{"model":"gemini-3.5-flash-high","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function"}]}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestAntigravityChatAcceptsBase64Image(t *testing.T) {
	handler := antigravityTestHandler(t, fakeAntigravityImageCommand(t))
	image := base64.StdEncoding.EncodeToString([]byte("fake-png"))
	body := `{"model":"gemini-3.5-flash-high","messages":[{"role":"user","content":[{"type":"text","text":"describe this"},{"type":"image_url","image_url":{"url":"data:image/png;base64,` + image + `"}}]}]}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "image received") {
		t.Fatalf("unexpected response %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestAntigravityChatRejectsRemoteImageURL(t *testing.T) {
	handler := antigravityTestHandler(t, fakeAntigravityCommand(t))
	body := `{"model":"gemini-3.5-flash-high","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/image.png"}}]}]}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "data:image") {
		t.Fatalf("unexpected response %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestOllamaChatForwardsImageContent(t *testing.T) {
	var forwarded []byte
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = io.WriteString(w, `{"models":[]}`)
		case "/v1/chat/completions":
			forwarded, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"seen"}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollamaServer.Close()
	handler := New(
		ollama.New(ollamaServer.URL),
		antigravity.New(filepath.Join(t.TempDir(), "missing-agy")),
		store.New(10),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	).Handler()
	body := `{"model":"llava","messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"data:image/png;base64,ZmFrZQ=="}}]}]}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
	if recorder.Code != http.StatusOK || !bytes.Equal(forwarded, []byte(body)) {
		t.Fatalf("Ollama request was not forwarded unchanged: status=%d body=%s forwarded=%s", recorder.Code, recorder.Body.String(), forwarded)
	}
}

func TestAntigravityChatStreamsOpenAIChunks(t *testing.T) {
	handler := antigravityTestHandler(t, fakeAntigravityCommand(t))
	body := `{"model":"gemini-3.5-flash-high","stream":true,"messages":[{"role":"user","content":"hello"}]}`
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
	if strings.Contains(modelsRecorder.Body.String(), "gemini-3.5-flash-high") {
		t.Fatalf("missing Antigravity CLI was exposed in model list: %s", modelsRecorder.Body.String())
	}

	body := `{"model":"gemini-3.5-flash-high","messages":[{"role":"user","content":"hello"}]}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "not installed") {
		t.Fatalf("expected missing CLI error, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestAntigravityModelsUseDirectAPIIDs(t *testing.T) {
	handler := antigravityTestHandler(t, fakeAntigravityCommand(t))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	body := recorder.Body.String()
	if !strings.Contains(body, `"id":"gemini-3.5-flash-high"`) || strings.Contains(body, "antigravity-cli/") {
		t.Fatalf("unexpected model list: %s", body)
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

func fakeAntigravityImageCommand(t *testing.T) string {
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
if [ ! -f nucleus-upload-1.png ]; then
  echo 'missing image' >&2
  exit 1
fi
printf 'image received\n'
`
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return command
}
