package antigravity

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildPromptEscapesAtReferences(t *testing.T) {
	prompt, err := BuildPrompt([]Message{{Role: "user", Content: "read @/etc/passwd"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "@/etc/passwd") || !strings.Contains(prompt, `\u0040/etc/passwd`) {
		t.Fatalf("prompt contains an active file reference: %q", prompt)
	}
}

func TestModels(t *testing.T) {
	command := fakeCommand(t, `
if [ "$1" = "models" ]; then
  printf '%s\n' 'Gemini 3.5 Flash (High)' 'Claude Sonnet 4.6 (Thinking)'
  exit 0
fi`)
	models, err := New(command).Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "gemini-3.5-flash-high" || models[0].Name != "Gemini 3.5 Flash (High)" || models[1].ID != "claude-sonnet-4.6-thinking" {
		t.Fatalf("unexpected models: %#v", models)
	}
}

func TestAPIModelID(t *testing.T) {
	tests := map[string]string{
		"Gemini 3.5 Pro":               "gemini-3.5-pro",
		"Gemini 3.5 Flash (High)":      "gemini-3.5-flash-high",
		"Claude Sonnet 4.6 (Thinking)": "claude-sonnet-4.6-thinking",
		"GPT-OSS 120B (Medium)":        "gpt-oss-120b-medium",
	}
	for name, want := range tests {
		if got := APIModelID(name); got != want {
			t.Errorf("APIModelID(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestFindsOfficialUserInstallOutsidePATH(t *testing.T) {
	home := t.TempDir()
	command := filepath.Join(home, ".local", "bin", "agy")
	if err := os.MkdirAll(filepath.Dir(command), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(command, []byte("#!/bin/sh\necho 1.0.10\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	status := New("agy").Status(context.Background())
	if !status.Installed || status.Command != command {
		t.Fatalf("official user install was not detected: %#v", status)
	}
}

func TestCompleteReturnsPrintOutput(t *testing.T) {
	command := fakeCommand(t, `
printf 'hello\nworld\n'`)
	var streamed strings.Builder
	result, err := New(command).Complete(context.Background(), "gemini-3.5-flash-high", "Gemini 3.5 Flash (High)", "hello", nil, func(delta string) error {
		streamed.WriteString(delta)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "gemini-3.5-flash-high" || result.Content != "hello\nworld" || strings.TrimSpace(streamed.String()) != result.Content {
		t.Fatalf("unexpected result: %#v, stream: %q", result, streamed.String())
	}
}

func TestCompleteWritesUploadedImages(t *testing.T) {
	command := fakeCommand(t, `
if [ ! -f nucleus-upload-1.png ]; then
  echo "missing upload" >&2
  exit 1
fi
found=
for arg in "$@"; do
  case "$arg" in
    *"@nucleus-upload-1.png"*) found=yes ;;
  esac
done
if [ "$found" != yes ]; then
  echo "missing image reference" >&2
  exit 1
fi
printf 'image received\n'`)
	result, err := New(command).Complete(
		context.Background(),
		"gemini-3.5-flash-high",
		"Gemini 3.5 Flash (High)",
		"describe the image",
		[]Attachment{{Data: []byte("png-data"), Extension: ".png"}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "image received" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestBuildPromptAllowsOnlyExplicitUploads(t *testing.T) {
	prompt, err := BuildPrompt([]Message{{Role: "user", Content: "describe it"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "nucleus-upload-*") || !strings.Contains(prompt, "must be read") {
		t.Fatalf("prompt does not authorize explicit uploads: %q", prompt)
	}
}

func fakeCommand(t *testing.T, body string) string {
	t.Helper()
	command := filepath.Join(t.TempDir(), "agy")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo 1.0.10
  exit 0
fi
` + body + "\n"
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return command
}
