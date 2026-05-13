package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
)

const launchAgentLabel = "ai.nucleus.local"

type Settings struct {
	LaunchAtLogin   bool `json:"launchAtLogin"`
	AutoCheckUpdate bool `json:"autoCheckUpdate"`
}

type Manager struct {
	path string
}

func New() *Manager {
	return &Manager{path: configPath()}
}

func (m *Manager) Get() Settings {
	cfg := Settings{AutoCheckUpdate: true}
	data, err := os.ReadFile(m.path)
	if err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	cfg.LaunchAtLogin = launchAgentInstalled()
	return cfg
}

func (m *Manager) Save(cfg Settings) error {
	if err := setLaunchAtLogin(cfg.LaunchAtLogin); err != nil {
		return err
	}
	cfg.LaunchAtLogin = launchAgentInstalled()
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(m.path, data, 0o644)
}

func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "nucleus-settings.json"
	}
	return filepath.Join(home, "Library", "Application Support", "Nucleus", "settings.json")
}

func launchAgentPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
}

func launchAgentInstalled() bool {
	path := launchAgentPath()
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func setLaunchAtLogin(enable bool) error {
	path := launchAgentPath()
	if path == "" {
		return errors.New("cannot resolve LaunchAgents directory")
	}
	if !enable {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	appPath, err := currentAppPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/bin/open</string>
    <string>%s</string>
  </array>
  <key>RunAtLoad</key><true/>
</dict>
</plist>
`, launchAgentLabel, html.EscapeString(appPath))
	return os.WriteFile(path, []byte(plist), 0o644)
}

func currentAppPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	parts := strings.Split(filepath.Clean(exe), string(filepath.Separator))
	for i := len(parts) - 1; i >= 0; i-- {
		if strings.HasSuffix(parts[i], ".app") {
			return string(filepath.Separator) + filepath.Join(parts[:i+1]...), nil
		}
	}
	return "", errors.New("launch at login is only available from Nucleus.app")
}
