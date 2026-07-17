package mascot

import (
	"math/rand"
	"strings"
	"testing"
)

func TestActivityFaceAndWorking(t *testing.T) {
	cases := []struct {
		a       Activity
		face    State
		working bool
	}{
		{ActReady, Idle, false},
		{ActThinking, Thinking, true},
		{ActReading, Thinking, true},
		{ActWriting, Thinking, true},
		{ActRunning, Thinking, true},
		{ActSuccess, Success, false},
		{ActError, Error, false},
	}
	for _, c := range cases {
		if c.a.Face() != c.face {
			t.Errorf("%v.Face() = %v, want %v", c.a, c.a.Face(), c.face)
		}
		if c.a.Working() != c.working {
			t.Errorf("%v.Working() = %v, want %v", c.a, c.a.Working(), c.working)
		}
	}
}

func TestCyclerThinkingKeepsSignatureCore(t *testing.T) {
	c := NewCycler(rand.New(rand.NewSource(1)))
	first := c.Set(ActThinking)
	if first != "sniffing…" {
		t.Errorf("thinking should start on the signature word, got %q", first)
	}
	// collect a run and check the signature trio dominates
	core := map[string]bool{"sniffing…": true, "tracking…": true, "on the scent…": true}
	coreCount := 0
	total := 20
	seen := []string{first}
	for i := 0; i < total-1; i++ {
		seen = append(seen, c.Next())
	}
	for _, w := range seen {
		if core[w] {
			coreCount++
		}
	}
	if coreCount < total/2 {
		t.Errorf("signature trio should dominate the thinking rotation, got %d/%d: %v", coreCount, total, seen)
	}
	// an extended word should appear too
	extra := false
	for _, w := range seen {
		if strings.HasPrefix(w, "digging") || strings.HasPrefix(w, "chewing") || strings.HasPrefix(w, "hunting") ||
			strings.HasPrefix(w, "nosing") || strings.HasPrefix(w, "pawing") || strings.HasPrefix(w, "rummaging") {
			extra = true
		}
	}
	if !extra {
		t.Errorf("thinking rotation should mix in an extended word over %d steps: %v", total, seen)
	}
}

func TestCyclerReadingCyclesPool(t *testing.T) {
	c := NewCycler(rand.New(rand.NewSource(2)))
	pool := Words[ActReading]
	if got := c.Set(ActReading); got != pool[0] {
		t.Errorf("first reading word = %q, want %q", got, pool[0])
	}
	if got := c.Next(); got != pool[1] {
		t.Errorf("second reading word = %q, want %q", got, pool[1])
	}
}

func TestCyclerReadyIsStatic(t *testing.T) {
	c := NewCycler(rand.New(rand.NewSource(3)))
	if got := c.Set(ActReady); got != "ready" {
		t.Errorf("ready word = %q, want ready", got)
	}
}
