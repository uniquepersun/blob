// Package prefs is the little settings file that survives restarts:
// os.UserConfigDir()/blob/settings.json. Errors are ignored everywhere;
// losing a preference must never stop the game.
package prefs

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Prefs is everything blob remembers.
type Prefs struct {
	Muted   bool   `json:"muted"`
	Style   string `json:"style,omitempty"`    // blob look; "" = random each run
	Shot    string `json:"shot,omitempty"`     // shot sound; "" = random tick/puff
	Bots    int    `json:"bots,omitempty"`     // 0 = random 3-8 each match
	GamePct int    `json:"game_pct,omitempty"` // wrapper panel height; 0 = default
	Side    bool   `json:"side,omitempty"`     // wrapper: game on the right
	Tipped  bool   `json:"tipped,omitempty"`   // saw the "esc opens the menu" welcome

	Speed   string `json:"speed,omitempty"`    // player speed: slow/normal/fast; "" = normal
	Skill   string `json:"skill,omitempty"`    // bots: gentle/normal/spicy; "" = normal
	Pickups string `json:"pickups,omitempty"`  // fewer/normal/more; "" = normal
	Volume  int    `json:"volume,omitempty"`   // percent; 0 = 75
	NoShake bool   `json:"no_shake,omitempty"` // screen shake off
	NoWords bool   `json:"no_words,omitempty"` // hit words off
}

func path() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "blob", "settings.json")
}

// Load returns the saved prefs, or zero values.
func Load() Prefs {
	var p Prefs
	if f := path(); f != "" {
		if b, err := os.ReadFile(f); err == nil {
			_ = json.Unmarshal(b, &p)
		}
	}
	return p
}

// Update loads, applies change, and saves, so callers only touch the field
// they care about.
func Update(change func(*Prefs)) {
	f := path()
	if f == "" {
		return
	}
	p := Load()
	change(&p)
	_ = os.MkdirAll(filepath.Dir(f), 0o755)
	if b, err := json.Marshal(p); err == nil {
		_ = os.WriteFile(f, b, 0o644)
	}
}
