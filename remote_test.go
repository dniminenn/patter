package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeToken(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte("test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func closedURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return "http://" + addr
}

func TestPostFallsThroughDialOnly(t *testing.T) {
	token := writeToken(t)
	var hits int
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"raw":"hi"}`))
	}))
	defer good.Close()

	dead := &remote{cfg: Config{Server: closedURL(t), Fallback: good.URL, TokenFile: token}}
	out, err := dead.post("/v1/transcribe", []byte("x"), "audio/wav")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "hi") || hits != 1 {
		t.Fatalf("hits=%d out=%s", hits, out)
	}

	hits = 0
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer bad.Close()
	live := &remote{cfg: Config{Server: bad.URL, Fallback: good.URL, TokenFile: token}}
	if _, err := live.post("/v1/sanitize", []byte("x"), "text/plain"); err == nil {
		t.Fatal("http 500 should stay on the server that answered")
	}
	if hits != 0 {
		t.Fatal("fallback ran after a live http error")
	}
}

func TestHealthFallsThrough(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer good.Close()
	r := &remote{cfg: Config{Server: closedURL(t), Fallback: good.URL}}
	if err := r.health(); err != nil {
		t.Fatal(err)
	}
}
