package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

type remote struct {
	cfg   Config
	token string
}

func (r *remote) readToken() error {
	b, err := os.ReadFile(r.cfg.TokenFile)
	if err != nil {
		return err
	}
	r.token = string(bytes.TrimSpace(b))
	if r.token == "" {
		return fmt.Errorf("empty token file")
	}
	return nil
}

func (r *remote) post(path string, body []byte, contentType string) ([]byte, error) {
	if err := r.readToken(); err != nil {
		return nil, err
	}
	var last error
	for _, base := range r.cfg.bases() {
		out, err := r.postOne(base+path, body, contentType)
		if err == nil {
			return out, nil
		}
		last = err
		if !dialFailed(err) {
			return nil, err
		}
		logf("server %s unreachable, trying next", base)
	}
	return nil, last
}

func (r *remote) postOne(url string, body []byte, contentType string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Content-Type", contentType)
	resp, err := httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(out))
	}
	return out, nil
}

func (r *remote) health() error {
	var last error
	for _, base := range r.cfg.bases() {
		resp, err := httpClient().Get(base + "/health")
		if err != nil {
			last = err
			if dialFailed(err) {
				continue
			}
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("health %s", resp.Status)
		}
		return nil
	}
	return last
}

func httpClient() *http.Client {
	return &http.Client{
		Timeout: 120 * time.Second,
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
			ResponseHeaderTimeout: 120 * time.Second,
		},
	}
}

func dialFailed(err error) bool {
	var op *net.OpError
	return errors.As(err, &op)
}

func pushSettings(cfg Config) error {
	tok, err := os.ReadFile(cfg.TokenFile)
	if err != nil {
		return err
	}
	token := string(bytes.TrimSpace(tok))
	if token == "" {
		return fmt.Errorf("empty token file")
	}
	body, err := json.Marshal(map[string]string{
		"asr":       cfg.speechModel(),
		"sanitizer": cfg.cleanupModel(),
		"language":  cfg.speechLanguage(),
	})
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout: 4 * time.Second,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
		},
	}
	var last error
	for _, base := range cfg.bases() {
		req, err := http.NewRequest(http.MethodPost, base+"/v1/settings", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			last = err
			if dialFailed(err) {
				continue
			}
			return err
		}
		out, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(out))
		}
		var parsed struct {
			RestartModels bool `json:"restart_models"`
		}
		_ = json.Unmarshal(out, &parsed)
		if parsed.RestartModels {
			logf("models saved for the next server start")
		}
		return nil
	}
	return last
}

func transcribePCM(r *remote, pcm []byte) (string, error) {
	out, err := r.post("/v1/transcribe", wavBytes(pcm), "audio/wav")
	if err != nil {
		return "", err
	}
	var body struct {
		Raw string `json:"raw"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return "", err
	}
	return trimSpace(body.Raw), nil
}

type sanitizeResult struct {
	Action string `json:"action"`
	Text   string `json:"text"`
	Raw    string `json:"raw"`
}

func sanitizeRaw(r *remote, raw string) (sanitizeResult, error) {
	out, err := r.post("/v1/sanitize", []byte(raw), "text/plain")
	if err != nil {
		return sanitizeResult{}, err
	}
	var body sanitizeResult
	if err := json.Unmarshal(out, &body); err != nil {
		return sanitizeResult{}, err
	}
	return body, nil
}
