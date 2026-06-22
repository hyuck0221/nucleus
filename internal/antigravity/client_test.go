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
	if strings.Join(models, ",") != "Gemini 3.5 Flash (High),Claude Sonnet 4.6 (Thinking)" {
		t.Fatalf("unexpected models: %#v", models)
	}
}

func TestCompleteReturnsPrintOutput(t *testing.T) {
	command := fakeCommand(t, `
printf 'hello\nworld\n'`)
	var streamed strings.Builder
	result, err := New(command).Complete(context.Background(), ModelID, "hello", func(delta string) error {
		streamed.WriteString(delta)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "hello\nworld" || strings.TrimSpace(streamed.String()) != result.Content {
		t.Fatalf("unexpected result: %#v, stream: %q", result, streamed.String())
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
