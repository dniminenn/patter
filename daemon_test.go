package main

import "testing"

func TestMarker(t *testing.T) {
	d := newDaemon(Config{Hotkey: "F9"})
	if m := d.marker(); m.ok || m.title != "Microphone" || m.detail != "The microphone is not open." {
		t.Fatal(m)
	}
	d.mic.flowing.Store(true)
	if m := d.marker(); m.ok || m.title != "Server" || m.detail != "The speech server is not answering." {
		t.Fatal(m)
	}
	d.serverOK.Store(true)
	if m := d.marker(); !m.ok || m.title != "Ready" || m.detail != "Hold F9 and speak." {
		t.Fatal(m)
	}
	d.keyHeld.Store(true)
	if d.marker().title != "Listening" {
		t.Fatal(d.marker())
	}
	d.busy.Store(true)
	if m := d.marker(); !m.ok || m.title != "Correcting" {
		t.Fatal(m)
	}
	d.busy.Store(false)
	d.keyHeld.Store(false)
	hotkeyArmed.Store(false)
	if m := d.marker(); m.ok || m.title != "Stopped" || m.detail != "Dictation is stopped." {
		t.Fatal(m)
	}
}

func TestIgnoreLongScratch(t *testing.T) {
	action, text := normalizeAction("scratch", "", "this is a long sentence here", "")
	if action != "insert" || text != "this is a long sentence here " {
		t.Fatalf("%s %q", action, text)
	}
	action, text = normalizeAction("scratch", "", "scratch that", "")
	if action != "scratch" || text != "" {
		t.Fatalf("%s %q", action, text)
	}
	action, text = normalizeAction("scratch", "", "Recyclotron.", "")
	if action != "insert" || text != "Recyclotron. " {
		t.Fatalf("%s %q", action, text)
	}
	action, text = normalizeAction("scratch", "", "Never mind.", "")
	if action != "scratch" || text != "" {
		t.Fatalf("%s %q", action, text)
	}
	action, text = normalizeAction("scratch", "", "", "one two three four five")
	if action != "insert" || text != "one two three four five " {
		t.Fatalf("%s %q", action, text)
	}
	action, text = normalizeAction("insert", "Meet on Wednesday. ", "um meet on tuesday no wednesday", "")
	if action != "insert" || text != "Meet on Wednesday. " {
		t.Fatalf("%s %q", action, text)
	}
}
