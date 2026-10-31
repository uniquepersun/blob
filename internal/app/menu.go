package app

import (
	"fmt"
	"math/rand"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/uniquepersun/blob/internal/game"
	"github.com/uniquepersun/blob/internal/prefs"
	"github.com/uniquepersun/blob/internal/render"
	"github.com/uniquepersun/blob/internal/sound"
)

// The esc menu: everything about the game in one place. Rows with a value
// change with ←/→ (or enter), and every change applies live and is saved.

type menuRow int

const (
	rowResume menuRow = iota
	rowLook
	rowSound
	rowShot
	rowBots
	rowSize   // wrapper only
	rowLayout // wrapper only
	rowControls
	rowNewMatch
	rowExit
)

const (
	maxBots        = 8
	minPct, maxPct = 40, 80
)

// cycle steps through choices (wrapping) from the current one.
func cycle(choices []string, cur string, dir int) string {
	i := 0
	for j, c := range choices {
		if c == cur {
			i = j
		}
	}
	n := len(choices)
	return choices[(i+n+dir)%n]
}

// randomOr shows a choice; unchosen ("") reads "random (what it rolled)".
func randomOr(choice, actual string) string {
	if choice == "" {
		return "random (" + actual + ")"
	}
	return choice
}

func (m *Model) menuRows() []menuRow {
	rows := []menuRow{rowResume, rowLook, rowSound, rowShot, rowBots}
	if m.wrap != nil {
		rows = append(rows, rowSize, rowLayout)
	}
	return append(rows, rowControls, rowNewMatch, rowExit)
}

func (m *Model) openMenu() {
	m.menu, m.menuSel, m.menuHelp = true, 0, false
	m.in.ClearKeys()
}

func (m *Model) menuView() *render.Menu {
	v := &render.Menu{
		Title: "blob",
		Sel:   m.menuSel,
		Hint:  "↑/↓ choose · ←/→ change · enter select · esc back",
	}
	if m.menuHelp {
		v.Title, v.Hint = "controls", "esc or enter: back"
		v.Help = []string{
			"w a s d          move",
			"mouse            aim · hold left to fire",
			"arrow keys       aim and fire",
			"space            fire",
			"e  b  right-click  throw a bomb",
			"p                pause",
			"m                sound on / off",
			"c                blob look",
			"esc              this menu",
		}
		if m.wrap != nil {
			v.Help = append(v.Help,
				"ctrl+]           show / hide the game",
				"click a pane     focus it")
		} else {
			v.Help = append(v.Help, "q                quit")
		}
		return v
	}
	for _, r := range m.menuRows() {
		v.Items = append(v.Items, m.menuItem(r))
	}
	return v
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func (m *Model) menuItem(r menuRow) render.MenuItem {
	switch r {
	case rowResume:
		return render.MenuItem{Label: "resume"}
	case rowLook:
		return render.MenuItem{Label: "blob look", Value: randomOr(m.cfg.Style, m.style.String())}
	case rowSound:
		return render.MenuItem{Label: "sound", Value: onOff(m.cfg.Sound != nil && !m.cfg.Sound.Muted())}
	case rowShot:
		return render.MenuItem{Label: "shot sound", Value: randomOr(m.cfg.Shot, m.cfg.Sound.Shot())}
	case rowBots:
		v := fmt.Sprint(m.cfg.Bots)
		if m.cfg.Bots == 0 {
			v = randomOr("", fmt.Sprint(len(m.world.Blobs)-1))
		}
		return render.MenuItem{Label: "bots", Value: v}
	case rowSize:
		return render.MenuItem{Label: "game size", Value: fmt.Sprintf("%d%%", m.cfg.GamePct), Off: m.cfg.Side && m.cols >= minSideCols}
	case rowLayout:
		v := "below"
		if m.cfg.Side {
			v = "right"
		}
		return render.MenuItem{Label: "game panel", Value: v, Off: m.cfg.Side && m.cols < minSideCols}
	case rowControls:
		return render.MenuItem{Label: "controls"}
	case rowNewMatch:
		return render.MenuItem{Label: "new match"}
	case rowExit:
		if m.wrap != nil {
			return render.MenuItem{Label: "hide game"}
		}
		return render.MenuItem{Label: "quit blob"}
	}
	return render.MenuItem{}
}

func (m *Model) onMenuKey(name string) tea.Cmd {
	if m.menuHelp {
		if name == "esc" || name == "enter" {
			m.menuHelp = false
		}
		return nil
	}
	rows := m.menuRows()
	switch name {
	case "esc":
		m.menu = false
	case "up", "w", "k":
		m.menuSel = (m.menuSel + len(rows) - 1) % len(rows)
	case "down", "s", "j", "tab":
		m.menuSel = (m.menuSel + 1) % len(rows)
	case "left", "a", "h":
		m.menuChange(rows[m.menuSel], -1)
	case "right", "d", "l":
		m.menuChange(rows[m.menuSel], 1)
	case "enter", "space":
		return m.menuSelect(rows[m.menuSel])
	}
	return nil
}

// menuChange steps a value row by dir (-1 or +1).
func (m *Model) menuChange(r menuRow, dir int) {
	switch r {
	case rowLook:
		choices := append([]string{""}, render.Styles...)
		c := cycle(choices, m.cfg.Style, dir)
		if st, ok := render.ParseStyle(c); ok {
			m.setStyle(st)
		} else { // back to random: roll now so you see it change
			m.cfg.Style = ""
			m.style = render.Style(rand.Intn(len(render.Styles)))
			prefs.Update(func(p *prefs.Prefs) { p.Style = "" })
		}
	case rowSound:
		m.toggleSound()
	case rowShot:
		m.cfg.Shot = cycle(append([]string{""}, sound.Shots...), m.cfg.Shot, dir)
		m.cfg.Sound.SetShot(sound.PickShot(m.cfg.Shot))
		m.cfg.Sound.Play(sound.Shoot) // hear it right away
		shot := m.cfg.Shot
		prefs.Update(func(p *prefs.Prefs) { p.Shot = shot })
	case rowBots: // random, 1..8 (0 = random)
		b := (m.cfg.Bots + maxBots + 1 + dir) % (maxBots + 1)
		m.cfg.Bots = b
		prefs.Update(func(p *prefs.Prefs) { p.Bots = b })
		m.newMatch()
	case rowSize:
		pct := min(maxPct, max(minPct, m.cfg.GamePct+5*dir))
		if pct != m.cfg.GamePct {
			m.cfg.GamePct = pct
			prefs.Update(func(p *prefs.Prefs) { p.GamePct = pct })
			m.relayout()
		}
	case rowLayout:
		m.cfg.Side = !m.cfg.Side
		side := m.cfg.Side
		prefs.Update(func(p *prefs.Prefs) { p.Side = side })
		m.relayout()
	}
}

func (m *Model) menuSelect(r menuRow) tea.Cmd {
	switch r {
	case rowResume:
		m.menu = false
	case rowControls:
		m.menuHelp = true
	case rowNewMatch:
		m.newMatch()
		m.menu = false
	case rowExit:
		m.menu = false
		if m.wrap == nil {
			return tea.Quit
		}
		m.showGame(false)
	default:
		m.menuChange(r, 1)
	}
	return nil
}

// newMatch starts over in the same arena with a fresh cast of bots (a new
// random count too, unless one is chosen).
func (m *Model) newMatch() {
	w, h := m.world.W, m.world.H
	m.world = game.NewWorld(w, h, matchBots(m.cfg.Bots), time.Now().UnixNano())
	m.in.ClearKeys()
}

func (m *Model) toggleSound() {
	if m.cfg.Sound == nil {
		return
	}
	muted := !m.cfg.Sound.Muted()
	m.cfg.Sound.SetMuted(muted)
	prefs.Update(func(p *prefs.Prefs) { p.Muted = muted })
}
