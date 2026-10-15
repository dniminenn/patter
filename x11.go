package main

/*
#cgo LDFLAGS: -lX11 -lXtst
#include <stdlib.h>
#include <X11/Xlib.h>
#include <X11/keysym.h>
#include <X11/extensions/XTest.h>
#include <X11/XKBlib.h>

static int patterXErr(Display *dpy, XErrorEvent *ev) {
    (void)dpy;
    (void)ev;
    return 0;
}

static void patterInstallErr(void) {
    XSetErrorHandler(patterXErr);
}

static int patterKeyDown(Display *dpy, unsigned int keycode) {
    char keys[32];
    XQueryKeymap(dpy, keys);
    return (keys[keycode / 8] & (1u << (keycode % 8))) != 0;
}

static int patterEventType(XEvent *ev) { return ev->type; }
static unsigned int patterEventKeycode(XEvent *ev) { return ev->xkey.keycode; }

static void patterGrab(Display *dpy, int keycode) {
    Window root = DefaultRootWindow(dpy);
    unsigned int mods[4] = {0, LockMask, Mod2Mask, LockMask | Mod2Mask};
    int i;
    for (i = 0; i < 4; i++) {
        XGrabKey(dpy, keycode, mods[i], root, False, GrabModeAsync, GrabModeAsync);
    }
    XSync(dpy, False);
}

static void patterUngrab(Display *dpy, int keycode) {
    Window root = DefaultRootWindow(dpy);
    unsigned int mods[4] = {0, LockMask, Mod2Mask, LockMask | Mod2Mask};
    int i;
    for (i = 0; i < 4; i++) {
        XUngrabKey(dpy, keycode, mods[i], root);
    }
    XSync(dpy, False);
}

static void patterKeymap(Display *dpy, char *keys) {
    XQueryKeymap(dpy, keys);
}

static void patterSetRepeat(Display *dpy, int keycode, int on) {
    XkbDescPtr desc = XkbAllocKeyboard();
    unsigned char cur, nxt, bit;
    int idx;
    if (!desc) {
        return;
    }
    if (XkbGetControls(dpy, XkbPerKeyRepeatMask, desc) != Success || !desc->ctrls) {
        XkbFreeKeyboard(desc, 0, True);
        return;
    }
    idx = keycode / 8;
    bit = (unsigned char)(1u << (keycode % 8));
    cur = desc->ctrls->per_key_repeat[idx];
    nxt = on ? (unsigned char)(cur | bit) : (unsigned char)(cur & ~bit);
    if (nxt != cur) {
        desc->ctrls->per_key_repeat[idx] = nxt;
        XkbSetControls(dpy, XkbPerKeyRepeatMask, desc);
        XFlush(dpy);
    }
    XkbFreeKeyboard(desc, 0, True);
}
*/
import "C"
import (
	"fmt"
	"time"
	"unsafe"
)

// keyThread owns one Display and the hotkey grab. It only reads X. On press
// it drops the active grab before any letter is typed, because that grab
// swallows the letters and the release then inserts only the suffix.
// If this goroutine returns, the caller must exit so the passive grab dies
// with the X connection.
func keyThread(hotkey string, events chan<- string, ready chan<- error, died chan struct{}) {
	var dpy *C.Display
	var kc C.int
	sent := false
	defer close(died)
	defer func() {
		if rec := recover(); rec != nil {
			logf("x thread panic: %v", rec)
		}
		if dpy != nil {
			C.patterSetRepeat(dpy, kc, 1)
			C.XCloseDisplay(dpy)
			dpy = nil
		}
		if !sent {
			select {
			case ready <- fmt.Errorf("x thread exited"):
			default:
			}
		}
		logf("x thread exit")
	}()
	C.patterInstallErr()
	dpy = C.XOpenDisplay(nil)
	if dpy == nil {
		sent = true
		ready <- fmt.Errorf("XOpenDisplay failed")
		return
	}
	cname := C.CString(hotkey)
	ks := C.XStringToKeysym(cname)
	C.free(unsafe.Pointer(cname))
	code := C.XKeysymToKeycode(dpy, ks)
	if code == 0 {
		sent = true
		ready <- fmt.Errorf("%s keycode missing", hotkey)
		return
	}
	kc = C.int(code)
	C.patterGrab(dpy, kc)
	C.patterSetRepeat(dpy, kc, 1)
	sent = true
	ready <- nil
	logf("x thread reading keycode %d", kc)
	current := hotkey
	wantHotkey.Store(current)
	held := C.patterKeyDown(dpy, C.uint(kc)) != 0
	grabbed := true
	var ev C.XEvent
	var keys, prev [32]byte
	C.patterKeymap(dpy, (*C.char)(unsafe.Pointer(&prev[0])))
	for {
		if !hotkeyArmed.Load() {
			if held {
				held = false
				C.patterSetRepeat(dpy, kc, 1)
				events <- "up"
			}
			if grabbed {
				C.patterUngrab(dpy, kc)
				C.patterSetRepeat(dpy, kc, 1)
				grabbed = false
				logf("paused")
			}
			time.Sleep(15 * time.Millisecond)
			continue
		}
		if !grabbed {
			C.patterGrab(dpy, kc)
			C.patterSetRepeat(dpy, kc, 1)
			grabbed = true
			C.patterKeymap(dpy, (*C.char)(unsafe.Pointer(&prev[0])))
			logf("armed")
		}
		if !held {
			rebindHotkey(dpy, &kc, &current)
		}
		for C.XPending(dpy) != 0 {
			C.XNextEvent(dpy, &ev)
			if held || C.patterEventType(&ev) != C.KeyPress || C.patterEventKeycode(&ev) != C.uint(kc) {
				continue
			}
			C.XUngrabKeyboard(dpy, C.CurrentTime)
			C.XFlush(dpy)
			C.patterSetRepeat(dpy, kc, 0)
			held = true
			logf("ungrab")
			events <- "down"
		}
		C.patterKeymap(dpy, (*C.char)(unsafe.Pointer(&keys[0])))
		noteForeignKeys(dpy, &prev, &keys, C.uint(kc))
		if held && C.patterKeyDown(dpy, C.uint(kc)) == 0 {
			held = false
			C.patterSetRepeat(dpy, kc, 1)
			events <- "up"
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func rebindHotkey(dpy *C.Display, kc *C.int, current *string) {
	next, _ := wantHotkey.Load().(string)
	if next == "" || next == *current {
		return
	}
	cname := C.CString(next)
	ks := C.XStringToKeysym(cname)
	C.free(unsafe.Pointer(cname))
	code := C.XKeysymToKeycode(dpy, ks)
	if code == 0 {
		logf("hotkey %s missing", next)
		wantHotkey.Store(*current)
		return
	}
	C.patterUngrab(dpy, *kc)
	*kc = C.int(code)
	C.patterGrab(dpy, *kc)
	C.patterSetRepeat(dpy, *kc, 1)
	*current = next
	logf("hotkey %s keycode %d", next, *kc)
}

func modifierKey(dpy *C.Display, code C.uint) bool {
	ks := C.XkbKeycodeToKeysym(dpy, C.KeyCode(code), 0, 0)
	switch ks {
	case 0xffe1, 0xffe2, 0xffe3, 0xffe4, 0xffe5, 0xffe6, 0xffe7, 0xffe8,
		0xffe9, 0xffea, 0xffeb, 0xffec, 0xffed, 0xffee, 0xff7e, 0xff7f, 0xfe03:
		return true
	default:
		return false
	}
}

func noteForeignKeys(dpy *C.Display, prev, keys *[32]byte, hot C.uint) {
	if injecting.Load() {
		*prev = *keys
		return
	}
	for i := 0; i < 32; i++ {
		diff := keys[i] &^ prev[i]
		if diff == 0 {
			continue
		}
		for b := 0; b < 8; b++ {
			if diff&(1<<uint(b)) == 0 {
				continue
			}
			code := C.uint(i*8 + b)
			if code < 8 || code == hot || modifierKey(dpy, code) {
				continue
			}
			foreignKeys.Add(1)
		}
	}
	*prev = *keys
}

type binding struct {
	code C.uint
	mods []C.KeySym
}

type typer struct {
	dpy *C.Display
	m   map[C.KeySym]binding
	ok  bool
}

func openTyper() *typer {
	dpy := C.XOpenDisplay(nil)
	if dpy == nil {
		logf("typer: no display")
		return &typer{}
	}
	t := &typer{dpy: dpy, m: map[C.KeySym]binding{}}
	C.XTestGrabControl(dpy, C.True)
	for code := C.uint(8); code < 256; code++ {
		for level := C.int(0); level < 4; level++ {
			ks := C.XkbKeycodeToKeysym(dpy, C.KeyCode(code), 0, level)
			if ks == 0 {
				continue
			}
			if _, seen := t.m[ks]; seen {
				continue
			}
			var mods []C.KeySym
			if level&1 != 0 {
				mods = append(mods, 0xffe1)
			}
			if level&2 != 0 {
				mods = append(mods, 0xfe03)
			}
			t.m[ks] = binding{code: code, mods: mods}
		}
	}
	C.XFlush(dpy)
	_, a := t.m[0x61]
	_, bs := t.m[0xff08]
	t.ok = a && bs
	if t.ok {
		logf("typer on")
	} else {
		logf("typer missing keys")
	}
	return t
}

func (t *typer) event(code C.uint, down bool) {
	v := C.int(0)
	if down {
		v = 1
	}
	C.XTestFakeKeyEvent(t.dpy, code, v, 0)
}

func (t *typer) mod(ks C.KeySym, down bool) {
	if b, ok := t.m[ks]; ok {
		t.event(b.code, down)
		return
	}
	code := C.XKeysymToKeycode(t.dpy, ks)
	if code != 0 {
		t.event(C.uint(code), down)
	}
}

func (t *typer) tap(ks C.KeySym) bool {
	b, ok := t.m[ks]
	if !ok {
		return false
	}
	for _, m := range b.mods {
		t.mod(m, true)
	}
	t.event(b.code, true)
	t.event(b.code, false)
	for i := len(b.mods) - 1; i >= 0; i-- {
		t.mod(b.mods[i], false)
	}
	return true
}

func (t *typer) typeText(s string) {
	if !t.ok {
		return
	}
	for _, ch := range s {
		ks := C.KeySym(ch)
		if ch == '\n' {
			ks = 0xff0d
		} else if ch > 255 {
			logf("no key for U+%04X", ch)
			continue
		}
		if !t.tap(ks) {
			logf("no key for %q", string(ch))
			continue
		}
		C.XFlush(t.dpy)
		time.Sleep(time.Millisecond)
	}
}

func (t *typer) backspace(n int) {
	if !t.ok {
		return
	}
	for i := 0; i < n; i++ {
		if !t.tap(0xff08) {
			logf("no backspace key")
			return
		}
		if i%8 == 7 {
			C.XFlush(t.dpy)
		}
	}
	C.XFlush(t.dpy)
}

func (t *typer) chord(shift bool, ks C.KeySym) bool {
	if !t.ok {
		return false
	}
	ctrl, okc := t.m[0xffe3]
	key, okk := t.m[ks]
	if !okc || !okk {
		return false
	}
	seq := []C.uint{ctrl.code}
	if shift {
		sh, oks := t.m[0xffe1]
		if !oks {
			return false
		}
		seq = append(seq, sh.code)
	}
	base := key.code
	if len(key.mods) > 0 && ks >= 65 && ks <= 90 {
		if plain, ok := t.m[ks+32]; ok {
			base = plain.code
		}
	}
	seq = append(seq, base)
	for _, c := range seq {
		t.event(c, true)
	}
	for i := len(seq) - 1; i >= 0; i-- {
		t.event(seq[i], false)
	}
	C.XFlush(t.dpy)
	return true
}
