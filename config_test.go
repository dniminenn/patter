package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATTER_CONFIG", filepath.Join(dir, "missing.json"))
	t.Setenv("HOME", dir)
	cfg := loadConfig()
	if cfg.Server != "http://127.0.0.1:8765" || cfg.Hotkey != "F9" {
		t.Fatal(cfg)
	}
	if cfg.TokenFile != filepath.Join(dir, ".config/patter/token") {
		t.Fatal(cfg.TokenFile)
	}
}

func TestLoadConfigFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	body := []byte(`{"server":"http://10.0.0.2:8765/","fallback":"http://100.1.1.1:8765","token_file":"~/secret","hotkey":"F8"}`)
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATTER_CONFIG", p)
	t.Setenv("HOME", dir)
	cfg := loadConfig()
	if cfg.Server != "http://10.0.0.2:8765" || cfg.Fallback != "http://100.1.1.1:8765" {
		t.Fatal(cfg)
	}
	if cfg.Hotkey != "F8" || cfg.TokenFile != filepath.Join(dir, "secret") {
		t.Fatal(cfg)
	}
	if len(cfg.bases()) != 2 {
		t.Fatal(cfg.bases())
	}
	if cfg.speechModel() != "Qwen/Qwen3-ASR-1.7B" || cfg.speechLanguage() != "English" {
		t.Fatal(cfg)
	}
}

func TestSaveKeepsHotkey(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	body := []byte("{\n  \"server\": \"http://10.0.0.2:8765\",\n  \"fallback\": \"\",\n  \"token_file\": \"~/secret\",\n  \"hotkey\": \"F9\"\n}\n")
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATTER_CONFIG", p)
	t.Setenv("HOME", dir)
	cfg := loadConfig()
	cfg.ASR = "Qwen/Qwen3-ASR-1.7B"
	cfg.Language = "English"
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	got := loadConfig()
	if got.Hotkey != "F9" || got.Server != "http://10.0.0.2:8765" {
		t.Fatal(got)
	}
	if got.speechModel() != "Qwen/Qwen3-ASR-1.7B" || got.speechLanguage() != "English" {
		t.Fatal(got)
	}
}
