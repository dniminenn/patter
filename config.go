package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Server    string `json:"server"`
	Fallback  string `json:"fallback"`
	TokenFile string `json:"token_file"`
	Hotkey    string `json:"hotkey"`
	Mic       string `json:"mic,omitempty"`
	ASR       string `json:"asr,omitempty"`
	Sanitizer string `json:"sanitizer,omitempty"`
	Language  string `json:"language,omitempty"`
}

func defaultConfig() Config {
	return Config{
		Server:    "http://127.0.0.1:8765",
		TokenFile: "~/.config/patter/token",
		Hotkey:    "F9",
	}
}

func configPath() string {
	if p := os.Getenv("PATTER_CONFIG"); p != "" {
		return expand(p)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "patter", "config.json")
}

func loadConfig() Config {
	cfg := defaultConfig()
	b, err := os.ReadFile(configPath())
	if err != nil {
		cfg.TokenFile = expand(cfg.TokenFile)
		return cfg
	}
	_ = json.Unmarshal(b, &cfg)
	if cfg.Server == "" {
		cfg.Server = defaultConfig().Server
	}
	if cfg.TokenFile == "" {
		cfg.TokenFile = defaultConfig().TokenFile
	}
	if cfg.Hotkey == "" {
		cfg.Hotkey = "F9"
	}
	cfg.Server = strings.TrimRight(cfg.Server, "/")
	cfg.Fallback = strings.TrimRight(cfg.Fallback, "/")
	cfg.TokenFile = expand(cfg.TokenFile)
	return cfg
}

func expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		if p == "~" {
			return home
		}
		return filepath.Join(home, p[2:])
	}
	return p
}

func saveConfig(cfg Config) error {
	path := configPath()
	cur := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &cur)
	}
	cur["server"] = cfg.Server
	cur["fallback"] = cfg.Fallback
	cur["hotkey"] = cfg.Hotkey
	cur["mic"] = cfg.Mic
	cur["asr"] = cfg.speechModel()
	cur["sanitizer"] = cfg.cleanupModel()
	cur["language"] = cfg.speechLanguage()
	if cur["token_file"] == nil || cur["token_file"] == "" {
		cur["token_file"] = "~/.config/patter/token"
	}
	b, err := json.MarshalIndent(cur, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func (c Config) speechModel() string {
	if s := strings.TrimSpace(c.ASR); s != "" {
		return s
	}
	return "Qwen/Qwen3-ASR-1.7B"
}

func (c Config) cleanupModel() string {
	if s := strings.TrimSpace(c.Sanitizer); s != "" {
		return s
	}
	return "Qwen/Qwen3-4B-Instruct-2507"
}

func (c Config) speechLanguage() string {
	if s := strings.TrimSpace(c.Language); s != "" {
		return s
	}
	return "English"
}

func (c Config) bases() []string {
	out := []string{c.Server}
	if c.Fallback != "" && c.Fallback != c.Server {
		out = append(out, c.Fallback)
	}
	return out
}
