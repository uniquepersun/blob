package sound

import (
	"math"
	"testing"
)

func TestEffectsAreShortCleanAndAudible(t *testing.T) {
	for _, shot := range Shots {
		checkEffects(t, shot)
	}
}

func checkEffects(t *testing.T, shot string) {
	fx := build(shot)
	for id, s := range fx {
		name := Names[id] + "/" + shot
		if ID(id) == Shoot && shot == "off" {
			continue
		}
		if len(s) == 0 || len(s) > rate*2 {
			t.Errorf("%s: %d samples", name, len(s))
			continue
		}
		peak := 0.0
		for _, v := range s {
			if math.IsNaN(float64(v)) {
				t.Fatalf("%s: NaN", name)
			}
			peak = math.Max(peak, math.Abs(float64(v)))
		}
		if peak < 0.05 || peak > 1.2 {
			t.Errorf("%s: peak %.2f", name, peak)
		}
		// Ends near silence, so effects never click when they stop.
		if tail := math.Abs(float64(s[len(s)-1])); tail > 0.02 {
			t.Errorf("%s: ends at %.3f", name, tail)
		}
	}
}

func TestMixerRateLimitsAndDrains(t *testing.T) {
	p := &Player{fx: varied(build("pew")), ready: true}
	p.Play(Shoot)
	p.Play(Shoot) // inside minGap: skipped
	if len(p.voices) != 1 {
		t.Fatalf("want 1 voice, got %d", len(p.voices))
	}
	buf := make([]byte, 4*rate) // one second
	if n, _ := p.Read(buf); n != len(buf) {
		t.Fatalf("read %d", n)
	}
	if len(p.voices) != 0 {
		t.Fatal("finished voices should be dropped")
	}
	p.SetMuted(true)
	p.Play(Boom)
	if len(p.voices) != 0 {
		t.Fatal("muted player queued a voice")
	}
}
