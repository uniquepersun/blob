package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/uniquepersun/blob/internal/ipc"
	"github.com/uniquepersun/blob/internal/render"
)

func TestOSCFilterDropsTitlesAcrossReads(t *testing.T) {
	in := "a\x1b]0;✳ Claude Code\x07b\x1b]2;x\x1b\\c\x1b]8;;http://x\x07link\x1b]8;;\x07\x1b[1md"
	want := "abc\x1b]8;;http://x\x07link\x1b]8;;\x07\x1b[1md"
	for split := 0; split <= len(in); split++ {
		var f oscFilter
		got := string(f.filter([]byte(in[:split]))) + string(f.filter([]byte(in[split:])))
		if got != want {
			t.Fatalf("split at %d:\n got %q\nwant %q", split, got, want)
		}
	}
}

func wrapped() *Model {
	m := New(Config{Agent: "claude", Argv: []string{"claude"}})
	m.cols, m.rows = 100, 30
	m.relayout()
	return m
}

func TestWrapperStartsHiddenAndNudges(t *testing.T) {
	m := wrapped()
	if m.open || m.focusGame || m.lay.agentH != 30 {
		t.Fatalf("want hidden game, full-height agent; open=%v focus=%v agentH=%d", m.open, m.focusGame, m.lay.agentH)
	}
	m.onAgent(ipc.Event{Kind: ipc.Working})
	if m.open || m.nudgeUntil <= m.now {
		t.Fatal("working should nudge, not open the game")
	}
}

func TestGamePanelTakesMostOfTheScreen(t *testing.T) {
	m := wrapped()
	m.showGame(true)
	if !m.focusGame || m.lay.gameH < 30*55/100 || m.lay.agentH+1+m.lay.gameH != 30 {
		t.Fatalf("focus=%v agentH=%d gameH=%d", m.focusGame, m.lay.agentH, m.lay.gameH)
	}
}

func TestAttentionHidesThenResumeReopens(t *testing.T) {
	m := wrapped()
	m.onAgent(ipc.Event{Kind: ipc.Working})
	m.showGame(true)
	m.onAgent(ipc.Event{Kind: ipc.Attention})
	if m.open || m.focusGame {
		t.Fatal("attention should hide the game and focus the agent")
	}
	m.onAgent(ipc.Event{Kind: ipc.Working})
	if !m.open || !m.focusGame {
		t.Fatal("answering should drop you back into the match")
	}
}

func TestAttentionWhileHiddenDoesNotOpen(t *testing.T) {
	m := wrapped()
	m.onAgent(ipc.Event{Kind: ipc.Working})
	m.onAgent(ipc.Event{Kind: ipc.Attention})
	m.onAgent(ipc.Event{Kind: ipc.Working})
	if m.open {
		t.Fatal("game opened without being asked")
	}
}

func TestDoneCelebratesThenHides(t *testing.T) {
	m := wrapped()
	m.onAgent(ipc.Event{Kind: ipc.Working})
	m.showGame(true)
	m.onAgent(ipc.Event{Kind: ipc.Done})
	if !m.open || m.focusGame || m.hideAt == 0 {
		t.Fatalf("want confetti visible with agent focused; open=%v focus=%v hideAt=%v", m.open, m.focusGame, m.hideAt)
	}
	m.onTick(m.start.Add(time.Duration((m.hideAt + 0.05) * float64(time.Second))))
	if m.open {
		t.Fatal("panel should close after the confetti")
	}
}

func press(m *Model, keys ...string) {
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "space":
			msg = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
		case "up":
			msg = tea.KeyPressMsg{Code: tea.KeyUp}
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		case "left":
			msg = tea.KeyPressMsg{Code: tea.KeyLeft}
		case "right":
			msg = tea.KeyPressMsg{Code: tea.KeyRight}
		default:
			msg = tea.KeyPressMsg{Code: rune(k[0]), Text: k}
		}
		m.onKeyPress(msg)
	}
}

func standalone(t *testing.T) *Model {
	t.Setenv("HOME", t.TempDir()) // prefs land in a temp dir
	m := New(Config{Style: "classic"})
	m.cols, m.rows = 100, 30
	m.relayout()
	return m
}

func TestUnchosenLookIsRandomAndThereIsNoBlockingPicker(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	seen := map[render.Style]bool{}
	for i := 0; i < 40; i++ {
		m := New(Config{})
		if m.picking {
			t.Fatal("first run should not block on a picker")
		}
		seen[m.style] = true
	}
	if len(seen) != len(render.Styles) {
		t.Fatalf("40 runs only ever drew %v", seen)
	}
}

func TestUnchosenBotsAreThreeToEight(t *testing.T) {
	counts := map[int]bool{}
	for i := 0; i < 200; i++ {
		n := matchBots(0)
		if n < 3 || n > 8 {
			t.Fatalf("drew %d bots", n)
		}
		counts[n] = true
	}
	if len(counts) != 6 || matchBots(5) != 5 {
		t.Fatalf("draws %v, chosen 5 -> %d", counts, matchBots(5))
	}
}

func TestWelcomeShowsOnceWhenGameFirstVisible(t *testing.T) {
	m := wrapped()
	t.Setenv("HOME", t.TempDir())
	m.onTick(m.start.Add(time.Second))
	if m.tipUntil != 0 {
		t.Fatal("welcome shown while the game was hidden")
	}
	m.showGame(true)
	m.onTick(m.start.Add(2 * time.Second))
	if m.tipUntil == 0 || !m.cfg.Tipped || !m.ui().Picker {
		t.Fatal("welcome should show the first time the game is on screen")
	}
}

func TestPickerIgnoresSpace(t *testing.T) {
	m := standalone(t)
	press(m, "c", "space")
	if !m.picking {
		t.Fatal("space (the fire key) dismissed the picker")
	}
	press(m, "right", "enter")
	if m.picking || m.style != render.StyleSimple || m.cfg.Style != "simple" {
		t.Fatalf("picking=%v style=%v choice=%q", m.picking, m.style, m.cfg.Style)
	}
}

func TestCOpensPickerAndEscCancels(t *testing.T) {
	m := standalone(t)
	press(m, "c", "right")
	if !m.picking || m.style != render.StyleSimple {
		t.Fatal("c should open the picker and preview live")
	}
	press(m, "esc")
	if m.picking || m.style != render.StyleClassic {
		t.Fatal("esc should cancel back to the old look")
	}
}

func TestEscMenuChangesBotsAndCloses(t *testing.T) {
	m := standalone(t)
	press(m, "esc")
	if !m.menu {
		t.Fatal("esc should open the menu")
	}
	rows := m.menuRows()
	for m.menuRows()[m.menuSel] != rowBots {
		press(m, "down")
	}
	if !strings.HasPrefix(m.menuView().Items[m.menuSel].Value, "random (") {
		t.Fatalf("unchosen bots should read random, got %q", m.menuView().Items[m.menuSel].Value)
	}
	press(m, "right", "right", "right", "right") // random -> 1 -> 2 -> 3 -> 4
	if m.cfg.Bots != 4 || len(m.world.Blobs) != 5 {
		t.Fatalf("bots=%d blobs=%d", m.cfg.Bots, len(m.world.Blobs))
	}
	if len(rows) != len(m.menuRows()) || m.menuView().Items[m.menuSel].Value != "4" {
		t.Fatal("menu should show the new bot count")
	}
	press(m, "esc")
	if m.menu {
		t.Fatal("esc should close the menu")
	}
}

func TestMenuGameSizeResizesPanel(t *testing.T) {
	m := wrapped()
	t.Setenv("HOME", t.TempDir())
	m.showGame(true)
	before := m.lay.gameH
	m.openMenu()
	for m.menuRows()[m.menuSel] != rowSize {
		press(m, "down")
	}
	press(m, "right", "right")
	if m.cfg.GamePct != 70 || m.lay.gameH <= before {
		t.Fatalf("pct=%d gameH %d -> %d", m.cfg.GamePct, before, m.lay.gameH)
	}
}
