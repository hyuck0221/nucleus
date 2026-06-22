package antigravity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

type Client struct {
	Command string
	mu      sync.Mutex
	status  Status
	models  []Model
	checked time.Time
}

type Status struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	Command   string `json:"command,omitempty"`
	Error     string `json:"error,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Result struct {
	Content string
	Model   string
}

type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func New(command string) *Client {
	if strings.TrimSpace(command) == "" {
		command = "agy"
	}
	return &Client{Command: command}
}

func (c *Client) Status(ctx context.Context) Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshLocked(ctx)
	return c.status
}

func (c *Client) Models(ctx context.Context) ([]Model, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshLocked(ctx)
	if !c.status.Installed {
		return nil, errors.New(c.status.Error)
	}
	if len(c.models) > 0 {
		return append([]Model(nil), c.models...), nil
	}
	path := c.status.Command
	out, err := commandContext(ctx, path, "models").CombinedOutput()
	if err != nil {
		return nil, errors.New(commandError(err, out))
	}
	for _, line := range strings.Split(string(out), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			if id := APIModelID(name); id != "" {
				c.models = append(c.models, Model{ID: id, Name: name})
			}
		}
	}
	return append([]Model(nil), c.models...), nil
}

func (c *Client) ResolveModel(ctx context.Context, id string) (Model, bool, error) {
	models, err := c.Models(ctx)
	if err != nil {
		return Model{}, false, err
	}
	for _, model := range models {
		if strings.EqualFold(model.ID, strings.TrimSpace(id)) {
			return model, true, nil
		}
	}
	return Model{}, false, nil
}

func (c *Client) refreshLocked(ctx context.Context) {
	if time.Since(c.checked) < 30*time.Second {
		return
	}
	path, err := resolveCommand(c.Command)
	if err != nil {
		c.status = Status{Error: err.Error()}
		c.models = nil
		c.checked = time.Now()
		return
	}
	status := Status{Installed: true, Command: path}
	out, err := commandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		status.Installed = false
		status.Error = commandError(err, out)
	} else {
		status.Version = strings.TrimSpace(string(out))
	}
	c.status = status
	c.models = nil
	c.checked = time.Now()
}

func APIModelID(name string) string {
	var out strings.Builder
	separator := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.':
			if separator && out.Len() > 0 {
				out.WriteByte('-')
			}
			out.WriteRune(r)
			separator = false
		default:
			separator = true
		}
	}
	return strings.Trim(out.String(), "-")
}

func IsPotentialModelID(model string) bool {
	value := strings.ToLower(strings.TrimSpace(model))
	for _, prefix := range []string{"gemini-", "claude-", "gpt-oss-"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func BuildPrompt(messages []Message) (string, error) {
	if len(messages) == 0 {
		return "", errors.New("messages must contain at least one text message")
	}
	data, err := json.Marshal(messages)
	if err != nil {
		return "", err
	}
	// Antigravity CLI expands @path references before the model sees the prompt.
	// JSON-escape @ so API input cannot attach or read a local file.
	conversation := strings.ReplaceAll(string(data), "@", `\u0040`)
	return "This is a text-only chat API request. Do not call tools, inspect the workspace, access files or URLs, run commands, use MCP, or delegate to agents. Answer the final message using only the conversation below. Reply only with the assistant answer.\n\nConversation JSON:\n" + conversation, nil
}

func (c *Client) Complete(ctx context.Context, apiModel, cliModel, prompt string, onDelta func(string) error) (Result, error) {
	status := c.Status(ctx)
	if !status.Installed {
		return Result{}, fmt.Errorf("Antigravity CLI is unavailable: %s", status.Error)
	}
	workDir, err := os.MkdirTemp("", "nucleus-antigravity-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(workDir)

	args := []string{"--sandbox", "--print-timeout", "5m", "--log-file", filepath.Join(workDir, "agy.log")}
	if strings.TrimSpace(cliModel) != "" {
		args = append(args, "--model", cliModel)
	}
	args = append(args, "--print", prompt)
	cmd := commandContext(ctx, status.Command, args...)
	cmd.Dir = workDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return Result{}, err
	}

	result := Result{Model: apiModel}
	reader := bufio.NewReader(stdout)
	var streamErr error
	for {
		chunk, readErr := reader.ReadString('\n')
		if chunk != "" {
			result.Content += chunk
			if onDelta != nil {
				if err := onDelta(chunk); err != nil {
					streamErr = err
					_ = cmd.Process.Kill()
					break
				}
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				streamErr = readErr
			}
			break
		}
	}
	waitErr := cmd.Wait()
	if streamErr != nil {
		return result, streamErr
	}
	if waitErr != nil {
		return result, errors.New(commandError(waitErr, stderr.Bytes()))
	}
	result.Content = strings.TrimSpace(result.Content)
	if result.Content == "" {
		return result, errors.New("Antigravity CLI returned an empty response")
	}
	return result, nil
}

func resolveCommand(command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		command = "agy"
	}
	if path, err := exec.LookPath(command); err == nil {
		return path, nil
	}
	if command != "agy" {
		return "", fmt.Errorf("%s was not found", command)
	}
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".local", "bin", "agy"),
		filepath.Join(home, "bin", "agy"),
		"/opt/homebrew/bin/agy",
		"/usr/local/bin/agy",
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", errors.New("agy was not found in PATH or a standard install location")
}

func commandContext(ctx context.Context, path string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(),
		"AGY_CLI_DISABLE_AUTO_UPDATE=true",
		"AGY_CLI_HIDE_ACCOUNT_INFO=true",
	)
	return cmd
}

func commandError(err error, output []byte) string {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return err.Error()
	}
	return fmt.Sprintf("%v: %s", err, message)
}
