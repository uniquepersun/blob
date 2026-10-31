package input

import (
	"testing"

	"github.com/uniquepersun/blob/internal/game"
)

func TestFallbackHoldSurvivesRepeatDelayThenStops(t *testing.T) {
	s := New()
	s.Press("d", 0, false)
	if !s.Held("d", 0.4) {
		t.Fatal("first press should bridge the OS repeat delay")
	}
	for tm := 0.45; tm < 1.0; tm += 0.05 { // auto-repeat stream
		s.Press("d", tm, false)
	}
	if !s.Held("d", 1.0) {
		t.Fatal("key should be held while repeats arrive")
	}
	if s.Held("d", 1.2) {
		t.Fatal("key should drop shortly after repeats stop")
	}
}

func TestPreciseModeUsesReleases(t *testing.T) {
	s := New()
	s.Precise = true
	s.Press("w", 0, false)
	if !s.Held("w", 100) {
		t.Fatal("precise mode holds until release")
	}
	s.Release("w")
	if s.Held("w", 100) {
		t.Fatal("release should let go")
	}
}

func TestArrowsAimAndFire(t *testing.T) {
	s := New()
	s.Precise = true
	s.Press("left", 0, false)
	c := s.Controls(&game.Blob{Pos: game.V(10, 10)}, 0)
	if !c.Fire || !c.HasAim || c.Aim.X >= 0 {
		t.Fatalf("left arrow should aim left and fire: %+v", c)
	}
}

func TestMouseAim(t *testing.T) {
	s := New()
	s.MouseMove(30, 10, 0)
	s.MouseButton(true, 0)
	c := s.Controls(&game.Blob{Pos: game.V(10, 10)}, 0.1)
	if !c.Fire || c.Aim.X <= 0.9 {
		t.Fatalf("mouse to the right should aim right and fire: %+v", c)
	}
}
