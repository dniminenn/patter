package main

import (
	"math"
	"strings"
)

const (
	minPCM       = 12800
	partialBytes = 32000
	prerollBytes = 12800
	firstPartial = 22400
	// One ASR forward stays at 5s. Longer holds commit finished pieces as text
	// so the audio convolution never sees a growing clip.
	chunkBytes   = 5 * 16000 * 2
	overlapBytes = 16000
)

func shownText(raw string) string {
	raw = trimSpace(raw)
	if raw == "" {
		return ""
	}
	return raw + " "
}

func trimSpace(s string) string {
	return string(trimRunes(s))
}

func trimRunes(s string) []rune {
	r := []rune(s)
	i, j := 0, len(r)
	for i < j && isSpace(r[i]) {
		i++
	}
	for j > i && isSpace(r[j-1]) {
		j--
	}
	return r[i:j]
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\n' || r == '\t' || r == '\r'
}

// revise returns how many trailing runes to delete and what to type instead.
func revise(old, new string) (back int, typed string) {
	or, nr := []rune(old), []rune(new)
	i := 0
	for i < len(or) && i < len(nr) && or[i] == nr[i] {
		i++
	}
	return len(or) - i, string(nr[i:])
}

func speechOK(raw string, nbytes int) bool {
	text := trimSpace(raw)
	if text == "" {
		return false
	}
	if nbytes < 1 {
		nbytes = 1
	}
	seconds := float64(nbytes) / 32000
	limit := int(seconds*64) + 12
	return len([]rune(text)) <= limit
}

func normWord(w string) string {
	return strings.Trim(strings.ToLower(w), ".,!?;:\"'")
}

func wordsEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if normWord(a[i]) != normWord(b[i]) {
			return false
		}
	}
	return true
}

// stitchText joins two transcripts that share a short overlap.
func stitchText(left, right string) string {
	left = trimSpace(left)
	right = trimSpace(right)
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	lw := strings.Fields(left)
	rw := strings.Fields(right)
	maxk := len(lw)
	if len(rw) < maxk {
		maxk = len(rw)
	}
	if maxk > 8 {
		maxk = 8
	}
	best := 0
	for k := maxk; k > 0; k-- {
		match := true
		for i := 0; i < k; i++ {
			if normWord(lw[len(lw)-k+i]) != normWord(rw[i]) {
				match = false
				break
			}
		}
		if match {
			best = k
			break
		}
	}
	if best == 0 {
		limit := len(rw)
		if limit > 12 {
			limit = 12
		}
		bestI, bestK := 0, 0
		for i := 1; i < limit; i++ {
			kmax := len(lw)
			if remain := len(rw) - i; remain < kmax {
				kmax = remain
			}
			if kmax > 8 {
				kmax = 8
			}
			for k := kmax; k >= 2; k-- {
				if wordsEq(lw[len(lw)-k:], rw[i:i+k]) {
					if k > bestK {
						bestK = k
						bestI = i
					}
					break
				}
			}
		}
		if bestK >= 2 {
			rw = rw[bestI+bestK:]
			if len(rw) == 0 {
				return left
			}
			return left + " " + strings.Join(rw, " ")
		}
	}
	rw = rw[best:]
	if len(rw) == 0 {
		return left
	}
	return left + " " + strings.Join(rw, " ")
}

// snapQuiet walks back from end looking for 200ms of quiet, so a cut
// lands in a pause. It returns end when the piece has no pause.
func snapQuiet(pcm []byte, start, end int) int {
	end = end &^ 1
	start = start &^ 1
	if end-start < 3*32000 {
		return end
	}
	const width = 6400
	const back = 32000
	earliest := end - back
	minCut := start + 3*32000
	if earliest < minCut {
		earliest = minCut
	}
	for pos := end; pos-width >= earliest; pos -= 640 {
		if pcmRMS(pcm[pos-width:pos]) < 220 {
			return pos &^ 1
		}
	}
	return end
}

func commitPoint(pcm []byte, start int) (cut int, overlap bool) {
	start = start &^ 1
	end := start + chunkBytes
	if end > len(pcm) {
		end = len(pcm)
	}
	end = end &^ 1
	quiet := snapQuiet(pcm, start, end)
	if quiet > start && quiet < end {
		return quiet, false
	}
	return end, true
}

func pcmRMS(pcm []byte) float64 {
	if len(pcm) < 2 {
		return 0
	}
	step := 2 * max(1, len(pcm)/4000)
	var acc float64
	n := 0
	for i := 0; i+1 < len(pcm); i += step {
		s := int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8)
		acc += float64(s) * float64(s)
		n++
	}
	if n == 0 {
		return 0
	}
	return math.Sqrt(acc / float64(n))
}

func wavBytes(pcm []byte) []byte {
	if len(pcm)%2 == 1 {
		pcm = pcm[:len(pcm)-1]
	}
	n := len(pcm)
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	put32(h[4:], uint32(36+n))
	copy(h[8:], "WAVE")
	copy(h[12:], "fmt ")
	put32(h[16:], 16)
	put16(h[20:], 1)
	put16(h[22:], 1)
	put32(h[24:], 16000)
	put32(h[28:], 32000)
	put16(h[32:], 2)
	put16(h[34:], 16)
	copy(h[36:], "data")
	put32(h[40:], uint32(n))
	return append(h, pcm...)
}

func put16(b []byte, v uint16) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
}

func put32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}
