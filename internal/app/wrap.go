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

	"github.com/abhay/blob/internal/ipc"
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

func (w *wrapper) write(b []byte) { _, _ = w.emu.Write(b) }

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
		var last uv.Style
		first := true
		for x := 0; x < w.cols; x++ {
			c := w.emu.CellAt(x, y)
			if c == nil {
				if !first && !last.IsZero() {
					sb.WriteString("\x1b[0m")
					last, first = uv.Style{}, false
				}
				sb.WriteByte(' ')
				continue
			}
			if c.Width == 0 {
				continue // trailing half of a wide character
			}
			if first || !c.Style.Equal(&last) {
				sb.WriteString("\x1b[0m")
				if !c.Style.IsZero() {
					sb.WriteString(c.Style.String())
				}
				last, first = c.Style, false
			}
			if c.Content == "" {
				sb.WriteByte(' ')
			} else {
				sb.WriteString(c.Content)
			}
		}
		sb.WriteString("\x1b[0m")
		out[y] = sb.String()
	}
	return out
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
