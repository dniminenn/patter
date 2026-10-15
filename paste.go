package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

var terminals = map[string]bool{
	"xfce4-terminal": true, "gnome-terminal": true, "gnome-terminal-server": true,
	"kitty": true, "alacritty": true, "xterm": true, "uxterm": true,
	"terminator": true, "konsole": true, "wezterm": true,
	"org.wezfurlong.wezterm": true, "st": true, "st-256color": true,
}

func copyqEnv() []string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = "/tmp"
	}
	env := os.Environ()
	return append(env, "COPYQ_SETTINGS_PATH="+base+"/copyq-session")
}

func windowClass() string {
	out, err := exec.Command("xdotool", "getactivewindow", "getwindowclassname").Output()
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(string(out)))
}

func clipboardIsText(text string) bool {
	script := "var want = str(arguments[1]);" +
		"var t = str(clipboard('text/plain'));" +
		"var p = clipboard('image/png');" +
		"var n = p ? p.length : 0;" +
		"print((t === want && n === 0) ? 'ok' : 'bad');"
	cmd := exec.Command("copyq", "eval", "--", script, text)
	cmd.Env = copyqEnv()
	out, err := runTimeout(cmd, 2*time.Second)
	if err != nil {
		logf("copyq read failed: %v", err)
		return false
	}
	return strings.TrimSpace(out) == "ok"
}

func setClipboardText(text string) bool {
	cmd := exec.Command("copyq", "copy", "text/plain", text, "UTF8_STRING", text)
	cmd.Env = copyqEnv()
	if _, err := runTimeout(cmd, 2*time.Second); err != nil {
		logf("copyq copy failed: %v", err)
		return false
	}
	return clipboardIsText(text)
}

func runTimeout(cmd *exec.Cmd, d time.Duration) (string, error) {
	var buf bytes.Buffer
	cmd.Stdout = &buf
	if err := cmd.Start(); err != nil {
		return "", err
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case err := <-wait:
		return buf.String(), err
	case <-timer.C:
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-wait
		return buf.String(), fmt.Errorf("timeout")
	}
}

func pasteText(t *typer, text string) {
	cls := windowClass()
	if setClipboardText(text) {
		shift := terminals[cls]
		combo := "ctrl+v"
		if shift {
			combo = "ctrl+shift+v"
		}
		logf("paste keys %s", combo)
		if t != nil && t.chord(shift, 0x76) {
			return
		}
		// No --clearmodifiers. That grabs the keyboard.
		_ = exec.Command("xdotool", "key", combo).Run()
		return
	}
	logf("typing into %s", cls)
	typeText(t, text)
}

func typeText(t *typer, text string) {
	if t != nil && t.ok {
		t.typeText(text)
		return
	}
	_ = exec.Command("xdotool", "type", "--delay", "1", text).Run()
}

func scratchN(t *typer, n int) {
	if n <= 0 {
		return
	}
	if t != nil && t.ok {
		t.backspace(n)
		return
	}
	for n > 0 {
		step := n
		if step > 80 {
			step = 80
		}
		_ = exec.Command("xdotool", "key", "--delay", "0", "--repeat", strconv.Itoa(step), "BackSpace").Run()
		n -= step
	}
}

func scratchText(t *typer, text string) {
	scratchN(t, len([]rune(text)))
}

func applyShown(t *typer, old, new string) {
	back, typed := revise(old, new)
	if back > 0 {
		scratchN(t, back)
	}
	if typed == "" {
		return
	}
	if len([]rune(typed)) > 48 || strings.Contains(typed, "\n") {
		pasteText(t, typed)
		return
	}
	typeText(t, typed)
}
