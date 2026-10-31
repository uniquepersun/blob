package app

import (
	"testing"
	"time"

	"github.com/abhay/blob/internal/ipc"
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
