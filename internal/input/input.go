// Package input turns terminal key and mouse events into game controls.
//
// Terminals that speak the Kitty keyboard protocol (kitty, Ghostty, WezTerm,
// recent Alacritty, foot, iTerm2) report key releases, so "hold W" works
// exactly like a real game. Everywhere else we only get presses plus the OS
// auto-repeat stream, so a key counts as held until its repeats stop
// arriving. Mouse buttons report releases in every terminal with SGR mouse
// mode, which makes mouse aim + hold-to-fire the reliable path everywhere.
package input

import (
	"github.com/abhay/blob/internal/game"
)

const (
	// Before the OS starts auto-repeating (usually 250–600ms), a single
	// press has to keep the key alive on its own.
	firstPressHold = 0.55
	// Once repeats are flowing, they arrive every ~30–60ms.
	repeatHold = 0.14
	// Mouse aim stays active this long after the last mouse movement.
	mouseAimLinger = 2.5
)

// State tracks which keys and buttons are down.
type State struct {
	Precise bool // true once the terminal confirmed key release reporting

	held  map[string]float64 // key -> expiry time (precise mode: +Inf)
	mouse struct {
		x, y      float64 // pixel coords inside the arena
		valid     bool
		down      bool
		lastMoved float64
	}
	bombQueued bool
}

func New() *State { return &State{held: map[string]float64{}} }

const forever = 1e18

// Press records a key press (or an auto-repeat) at time now.
func (s *State) Press(key string, now float64, isRepeat bool) {
	if s.Precise {
		s.held[key] = forever
		return
	}
	exp, wasHeld := s.held[key]
	if (isRepeat || wasHeld) && exp > now {
		s.held[key] = now + repeatHold
	} else {
		s.held[key] = now + firstPressHold
	}
}

// Release records a key release (precise mode only).
func (s *State) Release(key string) { delete(s.held, key) }

// Held reports whether a key is currently down.
func (s *State) Held(key string, now float64) bool {
	exp, ok := s.held[key]
	return ok && exp > now
}

// ClearKeys forgets all held keys (used when focus moves away from the game,
// so a key released elsewhere doesn't stay stuck down).
func (s *State) ClearKeys() {
	for k := range s.held {
		delete(s.held, k)
	}
	s.mouse.down = false
}

// QueueBomb asks for a bomb throw on the next step.
func (s *State) QueueBomb() { s.bombQueued = true }

// MouseMove updates the cursor position in arena pixels.
func (s *State) MouseMove(px, py, now float64) {
	s.mouse.x, s.mouse.y, s.mouse.valid = px, py, true
	s.mouse.lastMoved = now
}

// MouseButton sets the left button state.
func (s *State) MouseButton(down bool, now float64) {
	s.mouse.down = down
	s.mouse.lastMoved = now
}

// Controls computes the player's controls for this frame.
func (s *State) Controls(player *game.Blob, now float64) game.Controls {
	var c game.Controls
	if s.Held("w", now) {
		c.Move.Y--
	}
	if s.Held("s", now) {
		c.Move.Y++
	}
	if s.Held("a", now) {
		c.Move.X--
	}
	if s.Held("d", now) {
		c.Move.X++
	}
	c.Move = c.Move.Norm()

	// Arrow keys aim and fire (twin-stick on a keyboard).
	var arrows game.Vec
	if s.Held("up", now) {
		arrows.Y--
	}
	if s.Held("down", now) {
		arrows.Y++
	}
	if s.Held("left", now) {
		arrows.X--
	}
	if s.Held("right", now) {
		arrows.X++
	}
	switch {
	case arrows.Len() > 0:
		c.Aim, c.HasAim, c.Fire = arrows.Norm(), true, true
	case s.mouse.valid && (s.mouse.down || now-s.mouse.lastMoved < mouseAimLinger):
		d := game.V(s.mouse.x, s.mouse.y).Sub(player.Pos)
		if d.Len() > 0.5 {
			c.Aim, c.HasAim = d.Norm(), true
		}
	}
	if s.mouse.down || s.Held("space", now) {
		c.Fire = true
	}
	if s.bombQueued {
		c.ThrowBomb = true
		s.bombQueued = false
	}
	return c
}
