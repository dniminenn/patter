package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var errDaemonRunning = errors.New("daemon already running")

var (
	wantHotkey  atomic.Value
	hotkeyArmed atomic.Bool
	injecting   atomic.Bool
	foreignKeys atomic.Int64
	shownGen    atomic.Int64
)

func logf(format string, args ...any) {
	fmt.Printf(time.Now().Format("15:04:05")+" "+format+"\n", args...)
	_ = os.Stdout.Sync()
}

func runtimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	return fmt.Sprintf("/run/user/%d", os.Getuid())
}

func sockPath() string {
	return filepath.Join(runtimeDir(), "patter.sock")
}

func stateDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "patter")
}

func lastPath() string {
	return filepath.Join(stateDir(), "last.txt")
}

func saveLast(text string) {
	_ = os.MkdirAll(stateDir(), 0o700)
	_ = os.WriteFile(lastPath(), []byte(text), 0o600)
}

func loadLast() string {
	b, err := os.ReadFile(lastPath())
	if err != nil {
		return ""
	}
	return string(b)
}

func clipLog(s string, n int) string {
	r := []rune(trimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n])
}

// Only a short utterance with an undo word deletes the previous paste.
// Anything else the model labels scratch is inserted as heard.
func normalizeAction(action, text, heard, raw string) (string, string) {
	heard = trimSpace(heard)
	if heard == "" {
		heard = trimSpace(raw)
	}
	if action == "scratch" && !undoPhrase(heard) {
		return "insert", shownText(heard)
	}
	return action, text
}

func undoPhrase(s string) bool {
	words := strings.Fields(strings.ToLower(strings.NewReplacer(".", " ", ",", " ", "!", " ", "?", " ").Replace(s)))
	if len(words) == 0 || len(words) > 4 {
		return false
	}
	for _, w := range words {
		switch w {
		case "scratch", "undo", "delete", "erase", "cancel", "nevermind":
			return true
		}
	}
	return len(words) >= 2 && words[0] == "never" && words[1] == "mind"
}

type result struct {
	utt  int
	kind string
	text string
}

type daemon struct {
	cfg         Config
	cfgMu       sync.Mutex
	rem         *remote
	mic         *mic
	keys        *typer
	events      chan string
	results     chan result
	recording   atomic.Bool
	busy        atomic.Bool
	keyHeld     atomic.Bool
	serverOK    atomic.Bool
	serverState atomic.Value
	utt         atomic.Int64
	finalNeeded atomic.Bool
	shown       string
	nid         string
	lockFile    *os.File
}

func init() {
	hotkeyArmed.Store(true)
}

func newDaemon(cfg Config) *daemon {
	d := &daemon{
		cfg:     cfg,
		rem:     &remote{cfg: cfg},
		mic:     newMic(cfg.Mic),
		events:  make(chan string, 16),
		results: make(chan result, 32),
	}
	d.mic.changed = pokeWindow
	return d
}

func (d *daemon) config() Config {
	d.cfgMu.Lock()
	defer d.cfgMu.Unlock()
	return d.cfg
}

func (d *daemon) replaceConfig(cfg Config) {
	d.cfgMu.Lock()
	d.cfg = cfg
	d.cfgMu.Unlock()
	wantHotkey.Store(cfg.Hotkey)
	pokeWindow()
}

func (d *daemon) say(summary, body string) {
	args := []string{"-a", "Patter", "-e", "-t", "2500", "-p"}
	if d.nid != "" {
		args = append([]string{"-r", d.nid}, args...)
	}
	args = append(args, summary)
	if body != "" {
		args = append(args, body)
	}
	cmd := exec.Command("notify-send", args...)
	out, err := runTimeout(cmd, time.Second)
	if err != nil {
		logf("notify failed: %v", err)
		return
	}
	text := strings.TrimSpace(out)
	if text != "" && digitsOnly(text) {
		d.nid = text
	}
}

func digitsOnly(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (d *daemon) startRec() {
	if d.recording.Load() || d.busy.Load() {
		return
	}
	select {
	case <-d.mic.ready:
	default:
		logf("mic not flowing")
		d.say("Microphone failed", "Input is not open yet")
		return
	}
	n := int(d.utt.Add(1))
	d.shown = ""
	d.finalNeeded.Store(false)
	base := d.mic.begin()
	d.recording.Store(true)
	go d.stream(n, base)
	logf("recording preroll %d", base)
}

func (d *daemon) stream(utt, base int) {
	sent := base
	commitAt := 0
	committed := ""
	for int(d.utt.Load()) == utt && !d.finalNeeded.Load() {
		pcm := d.mic.snapshot()
		if commitAt > len(pcm) {
			commitAt = len(pcm) &^ 1
		}
		tail := pcm[commitAt:]
		if len(tail) >= chunkBytes {
			cut, overlapped := commitPoint(pcm, commitAt)
			piece := pcm[commitAt:cut]
			logf("commit bytes %d", len(piece))
			raw, err := transcribePCM(d.rem, piece)
			if err != nil || raw == "" || !speechOK(raw, len(piece)) {
				if err != nil {
					logf("commit failed: %v", err)
				} else if raw != "" {
					logf("commit dropped %d chars", len([]rune(trimSpace(raw))))
				}
				time.Sleep(400 * time.Millisecond)
				continue
			}
			committed = stitchText(committed, raw)
			next := cut
			if overlapped && cut-commitAt > overlapBytes {
				next = cut - overlapBytes
			}
			if next < commitAt {
				next = cut
			}
			commitAt = next &^ 1
			sent = len(pcm)
			if int(d.utt.Load()) == utt {
				logf("committed %d", len([]rune(committed)))
				d.results <- result{utt, "partial", committed}
			}
			continue
		}
		need := base + firstPartial
		if sent != base {
			need = sent + partialBytes
		}
		rmsSrc := tail
		if commitAt == 0 && len(pcm) > base {
			rmsSrc = pcm[base:]
		}
		if len(pcm) >= need && len(tail) >= minPCM && pcmRMS(rmsSrc) >= 350 {
			sent = len(pcm)
			logf("partial bytes %d tail %d", sent, len(tail))
			raw, err := transcribePCM(d.rem, tail)
			if err != nil {
				logf("partial failed: %v", err)
				continue
			}
			if int(d.utt.Load()) == utt && raw != "" && speechOK(raw, len(tail)) {
				shown := stitchText(committed, raw)
				logf("partial %d %s", len([]rune(trimSpace(shown))), clipLog(shown, 80))
				d.results <- result{utt, "partial", shown}
			} else if int(d.utt.Load()) == utt && raw != "" {
				logf("partial dropped %d chars from %d bytes", len([]rune(trimSpace(raw))), len(tail))
			}
			continue
		}
		time.Sleep(100 * time.Millisecond)
	}
	if int(d.utt.Load()) != utt {
		return
	}
	deadline := time.Now().Add(time.Second)
	for d.mic.capturingNow() && int(d.utt.Load()) == utt && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if int(d.utt.Load()) != utt {
		return
	}
	pcm := d.mic.snapshot()
	if commitAt > len(pcm) {
		commitAt = len(pcm) &^ 1
	}
	tail := pcm[commitAt:]
	rmsSrc := tail
	if commitAt == 0 && len(pcm) > base {
		rmsSrc = pcm[base:]
	}
	raw := committed
	if len(rmsSrc) >= minPCM && pcmRMS(rmsSrc) >= 350 {
		piece := tail
		if commitAt == 0 {
			piece = pcm
		}
		got, err := transcribePCM(d.rem, piece)
		if err != nil {
			if committed == "" {
				logf("transcribe failed: %v", err)
				d.results <- result{utt, "error", err.Error()}
				return
			}
			logf("tail transcribe failed, keeping committed: %v", err)
		} else if got != "" && (commitAt == 0 || speechOK(got, len(piece))) {
			raw = stitchText(committed, got)
		} else if got != "" {
			logf("final tail dropped %d chars", len([]rune(trimSpace(got))))
		}
	}
	if raw != "" {
		logf("final %d %s", len([]rune(trimSpace(raw))), clipLog(raw, 80))
	}
	d.results <- result{utt, "final", raw}
}

func userMovedOn() bool {
	return foreignKeys.Load() != shownGen.Load()
}

func (d *daemon) show(raw string) {
	next := shownText(raw)
	if next == d.shown {
		shownGen.Store(foreignKeys.Load())
		return
	}
	injecting.Store(true)
	applyShown(d.keys, d.shown, next)
	injecting.Store(false)
	d.shown = next
	shownGen.Store(foreignKeys.Load())
	logf("showing %d", len([]rune(next)))
}

func (d *daemon) correct(raw string) {
	raw = trimSpace(raw)
	if raw == "" {
		if d.shown == "" {
			d.say("Too short", "Hold F9 while you talk")
		}
		return
	}
	if d.shown != "" && userMovedOn() {
		logf("left the text alone, a key was pressed")
		saveLast(d.shown)
		return
	}
	d.show(raw)
	res, err := sanitizeRaw(d.rem, raw)
	if err != nil {
		logf("sanitize failed: %v", err)
		msg := err.Error()
		if len(msg) > 120 {
			msg = msg[:120]
		}
		d.say("Kept the transcript", msg)
		if d.shown != "" {
			saveLast(d.shown)
		}
		return
	}
	action, text := normalizeAction(res.Action, res.Text, res.Raw, raw)
	if res.Action == "scratch" && action != "scratch" {
		heard := trimSpace(res.Raw)
		if heard == "" {
			heard = raw
		}
		logf("ignored scratch, %d words", len(strings.Fields(heard)))
	}
	if d.shown != "" && userMovedOn() {
		logf("left the text alone, a key was pressed")
		saveLast(d.shown)
		return
	}
	if action == "scratch" {
		injecting.Store(true)
		if d.shown != "" {
			applyShown(d.keys, d.shown, "")
			d.shown = ""
		}
		last := loadLast()
		if last != "" {
			scratchText(d.keys, last)
			saveLast("")
			logf("scratched %d", len([]rune(last)))
			d.say("Scratched", "")
		} else {
			d.say("Nothing to scratch", "")
		}
		injecting.Store(false)
		shownGen.Store(foreignKeys.Load())
		return
	}
	if action != "insert" || text == "" {
		if d.shown == "" {
			d.say("Nothing heard", "")
		}
		return
	}
	d.show(text)
	saveLast(d.shown)
	logf("corrected %d", len([]rune(d.shown)))
}

func (d *daemon) onKey(ev string) {
	logf("%s", ev)
	defer pokeWindow()
	if ev == "down" {
		d.keyHeld.Store(true)
		if !d.busy.Load() && !d.recording.Load() {
			d.startRec()
		}
		return
	}
	if ev == "up" {
		d.keyHeld.Store(false)
		if d.recording.Load() && !d.busy.Load() {
			logf("release")
			d.finalNeeded.Store(true)
			d.mic.end()
			d.recording.Store(false)
		}
	}
}

func (d *daemon) take(res result) {
	if int(d.utt.Load()) != res.utt {
		return
	}
	switch res.kind {
	case "partial":
		logf("partial %d", len([]rune(res.text)))
		d.show(res.text)
	case "error":
		msg := res.text
		if len(msg) > 180 {
			msg = msg[:180]
		}
		d.say("Dictation failed", msg)
	case "final":
		d.busy.Store(true)
		pokeWindow()
		func() {
			defer func() {
				d.busy.Store(false)
				pokeWindow()
			}()
			d.correct(res.text)
		}()
		if d.keyHeld.Load() && !d.recording.Load() {
			d.startRec()
		}
	}
}

func (d *daemon) drain() {
	for {
		select {
		case ev := <-d.events:
			d.onKey(ev)
		case res := <-d.results:
			d.take(res)
		default:
			return
		}
	}
}

// acquire holds the daemon lock for the life of the process. A second
// daemon exits before it maps a window.
func (d *daemon) acquire() error {
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(stateDir(), "daemon.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return errDaemonRunning
	}
	d.lockFile = lock
	return nil
}

// restartWhenIdle re-execs after the current hold has released and its
// correction has landed, so a restart never cuts a sentence.
func restartWhenIdle() {
	d := uiDaemon
	if d == nil {
		return
	}
	go func() {
		for d.keyHeld.Load() || d.recording.Load() || d.busy.Load() {
			time.Sleep(50 * time.Millisecond)
		}
		restartDaemon()
	}()
}

// restartDaemon writes nothing. The caller saves first. The new process
// inherits this display and this log. The lock is released before the
// child starts so it can acquire.
func restartDaemon() {
	d := uiDaemon
	exe, err := os.Executable()
	if err != nil || d == nil {
		logf("restart: %v", err)
		return
	}
	lock := d.lockFile
	if lock != nil {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	}
	logf("restarting")
	cmd := exec.Command(exe, "daemon")
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		logf("restart: %v", err)
		if lock != nil {
			_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		}
		return
	}
	if lock != nil {
		_ = lock.Close()
		d.lockFile = nil
	}
	os.Exit(0)
}

// run grabs the hotkey, keeps the mic open, and types into the focused
// window. The key goroutine never calls this loop. If it returns, the
// process exits so a stuck grab cannot freeze the session.
func (d *daemon) run() {
	go d.watchHealth()
	ready := make(chan error, 1)
	died := make(chan struct{})
	go keyThread(d.cfg.Hotkey, d.events, ready, died)
	select {
	case err := <-ready:
		if err != nil {
			logf("%v", err)
			os.Exit(1)
		}
	case <-time.After(2 * time.Second):
		logf("x thread did not start")
		os.Exit(1)
	case <-died:
		logf("x thread died")
		os.Exit(1)
	}
	d.keys = openTyper()
	if d.keys == nil || !d.keys.ok {
		logf("typer fell back to xdotool")
	}
	d.mic.open()
	select {
	case <-d.mic.ready:
	case <-time.After(3 * time.Second):
		logf("mic not flowing yet")
	}
	_ = os.Remove(sockPath())
	ln, err := net.Listen("unix", sockPath())
	if err != nil {
		logf("socket: %v", err)
		os.Exit(1)
	}
	if err := os.Chmod(sockPath(), 0o600); err != nil {
		logf("socket mode: %v", err)
	}
	ul := ln.(*net.UnixListener)
	cfg := d.config()
	logf("server %s hotkey %s", cfg.Server, cfg.Hotkey)
	if cfg.Fallback != "" {
		logf("fallback %s", cfg.Fallback)
	}
	logf("ready hold %s", cfg.Hotkey)
	for {
		select {
		case <-died:
			logf("x thread died")
			os.Exit(1)
		default:
		}
		d.drain()
		_ = ul.SetDeadline(time.Now().Add(50 * time.Millisecond))
		conn, err := ul.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			logf("accept: %v", err)
			continue
		}
		d.answer(conn)
	}
}

func (d *daemon) watchHealth() {
	client := &http.Client{Timeout: 2 * time.Second}
	var seen, last bool
	for {
		ok := d.pollHealth(client, d.config())
		d.serverOK.Store(ok)
		if !seen || last != ok {
			if ok {
				logf("speech server ready")
			} else {
				logf("speech server not answering")
			}
			seen = true
			last = ok
		}
		pokeWindow()
		time.Sleep(2 * time.Second)
	}
}

// health is what /health last said. State is downloading, loading, failed,
// or empty when the server is ready or unreachable.
type health struct {
	State   string `json:"state"`
	Model   string `json:"model"`
	Percent int    `json:"percent"`
	Error   string `json:"error"`
}

func (d *daemon) pollHealth(client *http.Client, cfg Config) bool {
	d.serverState.Store(health{})
	for _, base := range cfg.bases() {
		resp, err := client.Get(base + "/health")
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		var h struct {
			Ready bool `json:"ready"`
			health
		}
		if json.Unmarshal(body, &h) != nil {
			continue
		}
		if h.Ready {
			return true
		}
		if h.State == "downloading" || h.State == "loading" || h.State == "failed" {
			d.serverState.Store(h.health)
			return false
		}
	}
	return false
}

func (d *daemon) answer(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 64)
	n, _ := conn.Read(buf)
	cmd := strings.TrimSpace(string(buf[:n]))
	state := "ok\n"
	if cmd == "show" {
		presentWindow()
		state = "shown\n"
	} else if cmd == "status" {
		state = "idle\n"
		if d.busy.Load() {
			state = "working\n"
		} else if d.recording.Load() {
			state = "recording\n"
		}
	}
	_, _ = conn.Write([]byte(state))
}

func sendCmd(cmd string) (string, error) {
	conn, err := net.DialTimeout("unix", sockPath(), 2*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte(cmd + "\n")); err != nil {
		return "", err
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if n == 0 && err != nil {
		return "", err
	}
	return strings.TrimSpace(string(buf[:n])), nil
}

func ensureDaemon() string {
	if s, err := sendCmd("status"); err == nil {
		return s
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	lf, err := os.OpenFile(filepath.Join(stateDir(), "daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cmd := exec.Command(exe, "daemon")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = lf
	cmd.Stderr = lf
	if err := cmd.Start(); err != nil {
		_ = lf.Close()
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = lf.Close()
	for i := 0; i < 50; i++ {
		time.Sleep(50 * time.Millisecond)
		if s, err := sendCmd("status"); err == nil {
			return s
		}
	}
	fmt.Fprintln(os.Stderr, "dictation daemon did not start")
	os.Exit(1)
	return ""
}
