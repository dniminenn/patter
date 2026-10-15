package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// mic stays open for the life of the daemon. Opening it on key-down clips
// the first syllable while the codec wakes.
type mic struct {
	mu        sync.Mutex
	ring      []byte
	utt       []byte
	frozen    []byte
	capturing bool
	ready     chan struct{}
	once      sync.Once
	stop      chan struct{}
	flowing   atomic.Bool
	changed   func()
	source    string
	warned    string
}

func newMic(source string) *mic {
	return &mic{ready: make(chan struct{}), stop: make(chan struct{}), source: source}
}

type source struct {
	Name string `json:"name"`
	Desc string `json:"description"`
}

// listSources returns the Pulse inputs, monitors left out.
func listSources() []source {
	out, err := exec.Command("pactl", "-f", "json", "list", "sources").Output()
	if err != nil {
		return nil
	}
	var all []source
	if json.Unmarshal(out, &all) != nil {
		return nil
	}
	var in []source
	for _, s := range all {
		if s.Name != "" && !strings.HasSuffix(s.Name, ".monitor") {
			in = append(in, s)
		}
	}
	return in
}

// pick is the configured source when Pulse has it, else the default.
// It runs on every open, so an unplugged device falls back and a
// replugged one is used again.
func (m *mic) pick() (string, error) {
	if m.source != "" {
		for _, s := range listSources() {
			if s.Name == m.source {
				m.warned = ""
				return s.Name, nil
			}
		}
		if m.warned != m.source {
			logf("mic %s missing, using default", m.source)
			m.warned = m.source
		}
	}
	out, err := exec.Command("pactl", "get-default-source").Output()
	if err != nil {
		return "", err
	}
	return trimSpace(string(out)), nil
}

func (m *mic) open() {
	go m.run()
}

func (m *mic) run() {
	errPath := os.Getenv("XDG_RUNTIME_DIR")
	if errPath == "" {
		errPath = "/tmp"
	}
	errPath += "/patter-mic.err"
	for {
		select {
		case <-m.stop:
			return
		default:
		}
		name, err := m.pick()
		if err != nil || name == "" {
			logf("no source: %v", err)
			time.Sleep(time.Second)
			continue
		}
		errf, _ := os.Create(errPath)
		cmd := exec.Command("ffmpeg",
			"-nostdin", "-hide_banner", "-loglevel", "error",
			"-fflags", "nobuffer",
			"-f", "pulse", "-i", name,
			"-ac", "1", "-ar", "16000", "-flush_packets", "1",
			"-f", "s16le", "pipe:1",
		)
		cmd.Stdin = nil
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			logf("mic pipe: %v", err)
			time.Sleep(time.Second)
			continue
		}
		cmd.Stderr = errf
		if err := cmd.Start(); err != nil {
			logf("mic start: %v", err)
			errf.Close()
			time.Sleep(time.Second)
			continue
		}
		logf("mic open %s", name)
		buf := make([]byte, 640)
		got := 0
		for {
			n, rerr := stdout.Read(buf)
			if n > 0 {
				chunk := append([]byte(nil), buf[:n]...)
				m.mu.Lock()
				m.ring = append(m.ring, chunk...)
				if extra := len(m.ring) - prerollBytes; extra > 0 {
					m.ring = append([]byte(nil), m.ring[extra:]...)
				}
				if m.capturing {
					m.utt = append(m.utt, chunk...)
				}
				m.mu.Unlock()
				got += n
				if got >= 3200 {
					m.setFlowing(true)
					m.once.Do(func() {
						logf("mic flowing")
						close(m.ready)
					})
				}
			}
			if rerr != nil {
				m.setFlowing(false)
				break
			}
		}
		_ = cmd.Wait()
		errf.Close()
		logf("mic dropped")
		time.Sleep(500 * time.Millisecond)
	}
}

func (m *mic) setFlowing(v bool) {
	if m.flowing.Swap(v) == v {
		return
	}
	if m.changed != nil {
		m.changed()
	}
}

func (m *mic) begin() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.utt = append([]byte(nil), m.ring...)
	m.frozen = nil
	m.capturing = true
	return len(m.utt)
}

func (m *mic) end() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.capturing = false
	m.frozen = append([]byte(nil), m.utt...)
}

func (m *mic) capturingNow() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.capturing
}

func (m *mic) snapshot() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	var src []byte
	if m.capturing {
		src = m.utt
	} else {
		src = m.frozen
	}
	out := append([]byte(nil), src...)
	if len(out)%2 == 1 {
		out = out[:len(out)-1]
	}
	return out
}
