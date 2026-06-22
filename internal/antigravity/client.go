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
)

const ModelID = "antigravity-cli"

type Client struct {
	Command string
	mu      sync.Mutex
	status  Status
	models  []string
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

func (c *Client) Models(ctx context.Context) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshLocked(ctx)
	if !c.status.Installed {
		return nil, errors.New(c.status.Error)
	}
	if len(c.models) > 0 {
		return append([]string(nil), c.models...), nil
	}
	path := c.status.Command
	out, err := commandContext(ctx, path, "models").CombinedOutput()
	if err != nil {
		return nil, errors.New(commandError(err, out))
	}
	for _, line := range strings.Split(string(out), "\n") {
		if model := strings.TrimSpace(line); model != "" {
			c.models = append(c.models, model)
		}
	}
	return append([]string(nil), c.models...), nil
}

func (c *Client) refreshLocked(ctx context.Context) {
	if time.Since(c.checked) < 30*time.Second {
		return
	}
	path, err := exec.LookPath(c.Command)
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

func IsModel(model string) bool {
	return model == ModelID || strings.HasPrefix(model, ModelID+"/")
}

func CLIModel(model string) string {
	return strings.TrimPrefix(model, ModelID+"/")
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

func (c *Client) Complete(ctx context.Context, model, prompt string, onDelta func(string) error) (Result, error) {
	path, err := exec.LookPath(c.Command)
	if err != nil {
		return Result{}, fmt.Errorf("Antigravity CLI is unavailable: %w", err)
	}
	workDir, err := os.MkdirTemp("", "nucleus-antigravity-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(workDir)

	args := []string{"--sandbox", "--print-timeout", "5m", "--log-file", filepath.Join(workDir, "agy.log")}
	if selected := CLIModel(model); selected != "" && selected != ModelID {
		args = append(args, "--model", selected)
	}
	args = append(args, "--print", prompt)
	cmd := commandContext(ctx, path, args...)
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

	result := Result{Model: model}
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
