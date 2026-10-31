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
	Style   string `json:"style,omitempty"`    // blob look; "" = never chosen
	Shot    string `json:"shot,omitempty"`     // shot sound; "" = default
	Bots    int    `json:"bots,omitempty"`     // 0 = default
	GamePct int    `json:"game_pct,omitempty"` // wrapper panel height; 0 = default
	Side    bool   `json:"side,omitempty"`     // wrapper: game on the right
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
