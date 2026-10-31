package app

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"

	"github.com/uniquepersun/blob/internal/ipc"
)

// Messages from the wrapper's goroutines into the Bubble Tea loop.
type (
	ptyOutMsg    []byte
	agentExitMsg struct{ err error }
)

// wrapper runs an agent CLI inside a pseudo-terminal and keeps a virtual
// screen of it, so we can draw the agent next to the game in one terminal.
// All emulator calls happen on the Bubble Tea goroutine (Update/View); the
// goroutines below only move bytes and post messages.
type wrapper struct {
	name string
	argv []string
	sock string

	cmd  *exec.Cmd
	ptmx *os.File
	emu  *vt.Emulator
	osc  oscFilter

	cols, rows int
}

func newWrapper(name string, argv []string, sock string) *wrapper {
	return &wrapper{name: name, argv: argv, sock: sock}
}

func (w *wrapper) started() bool { return w.cmd != nil }

// start launches the agent at the given size. send posts messages into the
// program (it is (*tea.Program).Send).
func (w *wrapper) start(cols, rows int, send func(tea.Msg)) error {
	w.cols, w.rows = cols, rows
	w.emu = vt.NewEmulator(cols, rows)
	w.emu.SetScrollbackSize(2000)

	w.cmd = exec.Command(w.argv[0], w.argv[1:]...)
	w.cmd.Env = append(os.Environ(),
		ipc.EnvSocket+"="+w.sock, // the agent's hooks talk to *this* game
		"BLOB_WRAPPED=1",
	)
	ptmx, err := pty.StartWithSize(w.cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		return err
	}
	w.ptmx = ptmx

	// Agent output -> emulator (via the update loop).
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				out := make([]byte, n)
				copy(out, buf[:n])
				send(ptyOutMsg(out))
			}
			if err != nil {
				return
			}
		}
	}()
	// Emulator replies and our forwarded keystrokes -> agent.
	go func() { _, _ = io.Copy(ptmx, w.emu) }()
	// Agent exit.
	go func() {
		err := w.cmd.Wait()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			err = nil // a normal non-zero exit is not our error to report
		}
		send(agentExitMsg{err})
	}()
	return nil
}

func (w *wrapper) write(b []byte) { _, _ = w.emu.Write(w.osc.filter(b)) }

// oscFilter drops window-title sequences (OSC 0/1/2) from the agent's
// output. x/vt v0.1.0 mis-parses titles containing non-ASCII (Claude Code
// sets ones like "✳ ...") and prints part of them into the screen. The
// title is ours to set anyway. Sequences can straddle reads, so it keeps
// state between calls.
type oscFilter struct {
	state byte // 0 text, 1 after ESC, 2 in OSC, 3 ESC inside OSC
	body  []byte
}

const maxOSC = 1 << 20

func (f *oscFilter) filter(in []byte) []byte {
	out := make([]byte, 0, len(in))
	for _, c := range in {
		switch f.state {
		case 0:
			if c == 0x1b {
				f.state = 1
			} else {
				out = append(out, c)
			}
		case 1:
			if c == ']' {
				f.state, f.body = 2, f.body[:0]
			} else {
				f.state = 0
				out = append(out, 0x1b, c)
			}
		case 2:
			switch {
			case c == 0x07:
				out = f.end(out, "\x07")
			case c == 0x1b:
				f.state = 3
			case len(f.body) >= maxOSC: // runaway: give up filtering it
				out = append(append(append(out, 0x1b, ']'), f.body...), c)
				f.state = 0
			default:
				f.body = append(f.body, c)
			}
		case 3:
			if c == '\\' {
				out = f.end(out, "\x1b\\")
			} else { // ESC that isn't ST: OSC ends, ESC starts a new sequence
				out = f.end(out, "")
				f.state = 1
				out = append(out, f.filter([]byte{c})...)
			}
		}
	}
	return out
}

func (f *oscFilter) end(out []byte, term string) []byte {
	f.state = 0
	if b := f.body; len(b) >= 2 && b[1] == ';' && (b[0] == '0' || b[0] == '1' || b[0] == '2') {
		return out // a title: drop it
	}
	out = append(append(out, 0x1b, ']'), f.body...)
	return append(out, term...)
}

func (w *wrapper) resize(cols, rows int) {
	if !w.started() || (cols == w.cols && rows == w.rows) {
		return
	}
	w.cols, w.rows = cols, rows
	w.emu.Resize(cols, rows)
	_ = pty.Setsize(w.ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

func (w *wrapper) sendKey(k tea.KeyPressMsg) { w.emu.SendKey(uv.KeyPressEvent(k)) }

func (w *wrapper) paste(s string) { w.emu.Paste(s) }

// sendMouse forwards a mouse event with coordinates relative to the pane.
// The emulator drops it unless the agent turned mouse reporting on.
func (w *wrapper) sendMouse(msg tea.MouseMsg, dx, dy int) {
	m := msg.Mouse()
	m.X -= dx
	m.Y -= dy
	switch msg.(type) {
	case tea.MouseClickMsg:
		w.emu.SendMouse(uv.MouseClickEvent(m))
	case tea.MouseReleaseMsg:
		w.emu.SendMouse(uv.MouseReleaseEvent(m))
	case tea.MouseWheelMsg:
		w.emu.SendMouse(uv.MouseWheelEvent(m))
	case tea.MouseMotionMsg:
		w.emu.SendMouse(uv.MouseMotionEvent(m))
	}
}

func (w *wrapper) focus(on bool) {
	if !w.started() {
		return
	}
	if on {
		w.emu.Focus()
	} else {
		w.emu.Blur()
	}
}

func (w *wrapper) kill() {
	if w.cmd != nil && w.cmd.Process != nil {
		_ = w.cmd.Process.Signal(os.Interrupt)
	}
	if w.ptmx != nil {
		_ = w.ptmx.Close()
	}
}

// lines renders the agent's screen as exactly rows strings of cols columns.
func (w *wrapper) lines() []string {
	out := make([]string, w.rows)
	var sb strings.Builder
	for y := 0; y < w.rows; y++ {
		sb.Reset()
		w.row(&sb, y, 0, w.cols)
		out[y] = sb.String()
	}
	return out
}

// rowRange renders columns [x0, x1) of one row, exactly x1-x0 columns wide.
func (w *wrapper) rowRange(y, x0, x1 int) string {
	var sb strings.Builder
	w.row(&sb, y, x0, x1)
	return sb.String()
}

// row writes columns [x0, x1) of row y. A wide character cut by either edge
// becomes spaces so the width always comes out exact.
func (w *wrapper) row(sb *strings.Builder, y, x0, x1 int) {
	var last uv.Style
	styled := false
	for x := x0; x < x1; {
		content, style, width := " ", uv.Style{}, 1
		if c := w.emu.CellAt(x, y); c != nil && c.Width > 0 && x+c.Width <= x1 {
			style, width = c.Style, c.Width
			if c.Content != "" {
				content = c.Content
			}
		}
		if !styled || !style.Equal(&last) {
			sb.WriteString("\x1b[0m")
			if !style.IsZero() {
				sb.WriteString(style.String())
			}
			last, styled = style, true
		}
		sb.WriteString(content)
		x += width
	}
	sb.WriteString("\x1b[0m")
}

// cursor returns the agent's cursor position and whether it is visible.
func (w *wrapper) cursor() (int, int, bool) {
	p := w.emu.CursorPosition()
	return p.X, p.Y, !w.emu.CursorHidden()
}

// Phrases agents use when they stop to ask you something. Matched against
// the bottom of the agent's screen as a fallback for agents without a
// permission hook (agy) or when hooks are not installed. Override with
// BLOB_PROMPT_REGEX.
var defaultPromptRe = regexp.MustCompile(`(?i)(do you want to (proceed|make this edit|create|run|allow)|allow (once|always|execution|this)|yes, and don't ask again|\(y/n\)|\[y/n\]|approve (this|the) (command|change|edit)|requires? (your )?approval|waiting for (your )?approval|permission to (run|use|execute))`)

func promptRegexp() *regexp.Regexp {
	if s := os.Getenv("BLOB_PROMPT_REGEX"); s != "" {
		if re, err := regexp.Compile(s); err == nil {
			return re
		}
	}
	return defaultPromptRe
}

// promptVisible reports whether an approval prompt is on the bottom part of
// the agent's screen.
func (w *wrapper) promptVisible(re *regexp.Regexp) bool {
	if !w.started() {
		return false
	}
	lines := strings.Split(w.emu.String(), "\n")
	from := max(0, len(lines)-14)
	return re.MatchString(strings.Join(lines[from:], "\n"))
}
