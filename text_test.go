package main

import "testing"

func TestSpeechOK(t *testing.T) {
	if speechOK(repeat('x', 107), 17568) {
		t.Fatal("short clip hallucination should drop")
	}
	if !speechOK(repeat('x', 18), 55968) {
		t.Fatal("18 chars of 1.7s should stay")
	}
	if !speechOK("hello there", 32000) {
		t.Fatal("hello")
	}
	if !speechOK(repeat('x', 140), 64000) {
		t.Fatal("boundary")
	}
	if speechOK("   ", 64000) {
		t.Fatal("blank")
	}
}

func TestRevise(t *testing.T) {
	back, typed := revise("All right. So is diction working? ", "All right. So is diction working now? ")
	if back != 2 || typed != " now? " {
		t.Fatalf("back=%d typed=%q", back, typed)
	}
	back, typed = revise("", "Okay. ")
	if back != 0 || typed != "Okay. " {
		t.Fatalf("first back=%d typed=%q", back, typed)
	}
}

func TestStitch(t *testing.T) {
	got := stitchText("the meeting is on Tuesday", "Tuesday and then Wednesday")
	if got != "the meeting is on Tuesday and then Wednesday" {
		t.Fatal(got)
	}
	got = stitchText("alpha bravo", "charlie delta")
	if got != "alpha bravo charlie delta" {
		t.Fatal(got)
	}
	if stitchText("", "only") != "only" || stitchText("only", "") != "only" {
		t.Fatal("empty side")
	}
	got = stitchText(
		"sometimes the trailing part of my sentence",
		"um the trailing part of my sentence gets repeated",
	)
	if got != "sometimes the trailing part of my sentence gets repeated" {
		t.Fatal(got)
	}
}

func TestSnapQuiet(t *testing.T) {
	pcm := make([]byte, chunkBytes)
	// 200ms of silence ending 0.4s before the cut. The rest is loud.
	quietAt := chunkBytes - 12800
	for i := 0; i < len(pcm); i += 2 {
		if i >= quietAt-6400 && i < quietAt {
			continue
		}
		pcm[i] = 0xff
		pcm[i+1] = 0x0f
	}
	cut, overlap := commitPoint(pcm, 0)
	if overlap || cut < quietAt-6400 || cut > quietAt {
		t.Fatalf("cut=%d overlap=%v quietAt=%d", cut, overlap, quietAt)
	}
	loud := make([]byte, chunkBytes)
	for i := 0; i < len(loud); i += 2 {
		loud[i] = 0xff
		loud[i+1] = 0x0f
	}
	cut, overlap = commitPoint(loud, 0)
	if !overlap || cut != chunkBytes {
		t.Fatalf("hard cut=%d overlap=%v", cut, overlap)
	}
}

func TestShown(t *testing.T) {
	if shownText("  Okay.") != "Okay. " {
		t.Fatal(shownText("  Okay."))
	}
	if shownText("  ") != "" {
		t.Fatal("spaces")
	}
}

func TestWAV(t *testing.T) {
	b := wavBytes([]byte{1, 2, 3})
	if string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		t.Fatal("header")
	}
	if len(b) != 44+2 {
		t.Fatal(len(b))
	}
}

func TestRMS(t *testing.T) {
	if pcmRMS(make([]byte, 8000)) != 0 {
		t.Fatal("silence")
	}
	pcm := make([]byte, 4000)
	for i := 0; i < len(pcm); i += 2 {
		pcm[i] = 0x40
		pcm[i+1] = 0x1f
	}
	if pcmRMS(pcm) < 1000 {
		t.Fatal(pcmRMS(pcm))
	}
}

func repeat(r rune, n int) string {
	s := make([]rune, n)
	for i := range s {
		s[i] = r
	}
	return string(s)
}
