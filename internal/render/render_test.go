package render

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/uniquepersun/blob/internal/game"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func visible(s string) int { return utf8.RuneCountInString(ansi.ReplaceAllString(s, "")) }

func TestFramesAreExactlyThePaneSize(t *testing.T) {
	r := NewRenderer()
	long := "wants to use Bash to run a very long command that keeps going and going past the edge"
	for _, size := range [][2]int{{24, 10}, {40, 12}, {57, 34}, {100, 32}, {200, 60}, {10, 5}} {
		cols, rows := size[0], size[1]
		aw, ah := ArenaSize(cols, rows)
		w := game.NewWorld(float64(aw), float64(ah), 3, 1)
		for i := 0; i < 120; i++ {
			w.Step()
		}
		menu := &Menu{Title: "blob", Hint: "↑/↓ choose · ←/→ change · enter select · esc back"}
		for i := 0; i < 16; i++ { // more rows than small panes hold: must scroll
			menu.Items = append(menu.Items, MenuItem{Label: "setting", Value: "random (classic)"})
		}
		menu.Sel = 12
		for _, paused := range []bool{false, true} {
			ui := UI{Time: 1, Paused: paused, PauseTitle: "◆ claude needs you", PauseBody: long,
				PauseHint: "answer it · the game waits for you", PauseAccent: game.ColCoral,
				StatusIcon: "⠋", StatusText: "claude working", Hints: strings.Repeat("hint ", 50)}
			if paused {
				ui.Menu = menu
			}
			for _, blocky := range []bool{false, true} {
				ui.Blocky = blocky
				lines := r.Render(w, ui, cols, rows)
				if len(lines) != rows {
					t.Fatalf("%dx%d paused=%v: %d lines", cols, rows, paused, len(lines))
				}
				for i, l := range lines {
					if v := visible(l); v != cols {
						t.Fatalf("%dx%d paused=%v blocky=%v line %d: width %d", cols, rows, paused, blocky, i, v)
					}
					if blocky && strings.Contains(l, "▀") {
						t.Fatalf("%dx%d blocky line %d uses ▀ (macOS Terminal draws it with a gap)", cols, rows, i)
					}
				}
			}
		}
	}
}

func TestWrap(t *testing.T) {
	got := wrap("one two three four", 9)
	want := []string{"one two", "three", "four"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
	for _, l := range wrap("averyveryverylongword", 5) {
		if Width(l) > 5 {
			t.Fatalf("line too wide: %q", l)
		}
	}
}
