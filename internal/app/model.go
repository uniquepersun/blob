// Package app is the Bubble Tea program: the game on its own, or the game
// side by side with an agent running in a pseudo-terminal.
package app

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/abhay/blob/internal/game"
	"github.com/abhay/blob/internal/input"
	"github.com/abhay/blob/internal/ipc"
	"github.com/abhay/blob/internal/render"
)

const (
	frameTime   = time.Second / 30
	scanEvery   = 0.3 // seconds between prompt scans of the agent screen
	donePauseIn = 1.3 // let the celebration confetti fly before freezing
	switchKey   = "ctrl+]"
	minSideCols = 110 // below this the wrapper stacks agent above game
)

type (
	tickMsg  time.Time
	agentMsg ipc.Event
)

type pauseReason int

const (
	notPaused pauseReason = iota
	pausedManual
	pausedAttention
	pausedDone
	pausedWaiting // wrapper start: waiting for the first task
)

// Config is how main starts the app.
type Config struct {
	Socket string
	Agent  string   // wrapper mode: display name ("claude")
	Argv   []string // wrapper mode: command to run
	Bots   int
}

// Model is the Bubble Tea model. It is used through a pointer so the
// goroutines started by the wrapper can reach the program.
type Model struct {
	cfg  Config
	send func(tea.Msg)

	cols, rows int
	lay        layout

	world *game.World
	rend  *render.Renderer
	in    *input.State

	start   time.Time
	last    time.Time
	acc     float64
	now     float64
	precise bool

	agentName  string
	agentKind  ipc.Kind
	agentMsg   string
	pause      pauseReason
	pauseAt    float64 // when a pending done-pause kicks in (0 = none)
	focusGame  bool
	wrap       *wrapper
	promptRe   *regexp.Regexp
	nextScan   float64
	scanHits   int
	scanSaw    bool
	recentKeys []string
	toast      string
	toastUntil float64
	exitErr    error
}

type layout struct {
	side                           bool
	agentX, agentY, agentW, agentH int
	gameX, gameY, gameW, gameH     int
}

// New builds the model.
func New(cfg Config) *Model {
	if cfg.Bots == 0 {
		cfg.Bots = 3
	}
	m := &Model{
		cfg:       cfg,
		world:     game.NewWorld(80, 40, cfg.Bots, time.Now().UnixNano()),
		rend:      render.NewRenderer(),
		in:        input.New(),
		start:     time.Now(),
		focusGame: true,
		agentName: cfg.Agent,
		promptRe:  promptRegexp(),
	}
	m.last = m.start
	if len(cfg.Argv) > 0 {
		m.wrap = newWrapper(cfg.Agent, cfg.Argv, cfg.Socket)
		m.focusGame = false
		m.pause = pausedWaiting
	}
	return m
}

// SetSend gives the model a way to post messages from other goroutines.
func (m *Model) SetSend(send func(tea.Msg)) { m.send = send }

// AgentEvent wraps an ipc event as a program message.
func AgentEvent(ev ipc.Event) tea.Msg { return agentMsg(ev) }

// Err is set if the wrapped agent could not start.
func (m *Model) Err() error { return m.exitErr }

func tick() tea.Cmd {
	return tea.Tick(frameTime, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) Init() tea.Cmd { return tick() }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.cols, m.rows = msg.Width, msg.Height
		m.relayout()
		if m.wrap != nil && !m.wrap.started() && m.send != nil {
			if err := m.wrap.start(m.lay.agentW, m.lay.agentH, m.send); err != nil {
				m.exitErr = fmt.Errorf("could not start %s: %w", strings.Join(m.cfg.Argv, " "), err)
				return m, tea.Quit
			}
		}

	case tea.KeyboardEnhancementsMsg:
		m.precise = msg.SupportsEventTypes()
		m.in.Precise = m.precise

	case tickMsg:
		m.onTick(time.Time(msg))
		return m, tick()

	case agentMsg:
		m.onAgent(ipc.Event(msg))

	case ptyOutMsg:
		if m.wrap != nil && m.wrap.started() {
			m.wrap.write(msg)
		}

	case agentExitMsg:
		m.exitErr = msg.err
		return m, tea.Quit

	case tea.BlurMsg:
		m.in.ClearKeys() // a key released in another window must not stick

	case tea.PasteMsg:
		if m.agentFocused() {
			m.wrap.paste(msg.Content)
		}

	case tea.KeyPressMsg:
		return m, m.onKeyPress(msg)

	case tea.KeyReleaseMsg:
		if m.focusGame {
			m.in.Release(keyName(tea.Key(msg)))
		}

	case tea.MouseMsg:
		m.onMouse(msg)
	}
	return m, nil
}

func (m *Model) agentFocused() bool { return m.wrap != nil && !m.focusGame && m.wrap.started() }

func (m *Model) relayout() {
	l := layout{}
	if m.wrap == nil {
		l.gameW, l.gameH = m.cols, m.rows
	} else if m.cols >= minSideCols {
		l.side = true
		l.agentW = m.cols * 56 / 100
		l.agentH = m.rows
		l.gameX = l.agentW + 1
		l.gameW = m.cols - l.gameX
		l.gameH = m.rows
	} else {
		l.agentW = m.cols
		l.agentH = max(6, m.rows*55/100)
		l.gameY = l.agentH + 1
		l.gameW = m.cols
		l.gameH = max(1, m.rows-l.gameY)
	}
	m.lay = l
	aw, ah := render.ArenaSize(l.gameW, l.gameH)
	m.world.Resize(float64(aw), float64(ah))
	if m.wrap != nil {
		m.wrap.resize(l.agentW, l.agentH)
	}
}

func (m *Model) onTick(t time.Time) {
	m.now = t.Sub(m.start).Seconds()
	dt := math.Min(t.Sub(m.last).Seconds(), 0.25)
	m.last = t

	if m.pauseAt > 0 && m.now >= m.pauseAt {
		m.pauseAt = 0
		m.setPause(pausedDone)
	}

	if m.wrap != nil && m.now >= m.nextScan {
		m.nextScan = m.now + scanEvery
		m.scanPrompt()
	}

	if m.pause != notPaused {
		m.acc = 0
		return
	}
	m.acc += dt
	for m.acc >= game.StepDT {
		if m.focusGame {
			m.world.Player().In = m.in.Controls(m.world.Player(), m.now)
		} else {
			m.world.Player().In = game.Controls{}
		}
		m.world.Step()
		m.acc -= game.StepDT
	}
}

// scanPrompt watches the agent's screen for approval prompts. It is the
// fallback for agents without a permission hook, and it also notices when
// a prompt goes away after you answer it.
func (m *Model) scanPrompt() {
	if m.wrap.promptVisible(m.promptRe) {
		m.scanHits = min(m.scanHits+1, 3)
	} else {
		m.scanHits = max(m.scanHits-1, -3)
	}
	switch {
	case m.scanHits >= 2 && m.agentKind != ipc.Attention:
		m.scanSaw = true
		m.onAgent(ipc.Event{Agent: m.agentName, Kind: ipc.Attention, Message: "is asking you something"})
	case m.scanHits >= 2:
		m.scanSaw = true
	case m.scanHits <= -2 && m.scanSaw && m.agentKind == ipc.Attention:
		m.scanSaw = false
		m.onAgent(ipc.Event{Agent: m.agentName, Kind: ipc.Working})
	}
}

func (m *Model) onAgent(ev ipc.Event) {
	// Standalone: show whichever agent last spoke. Wrapper: keep the name of
	// the agent we launched.
	if ev.Agent != "" && (m.wrap == nil || m.agentName == "") {
		m.agentName = ev.Agent
	}
	prev := m.agentKind
	switch ev.Kind {
	case ipc.Attention:
		m.agentKind, m.agentMsg = ipc.Attention, ev.Message
		m.pauseAt = 0
		m.setPause(pausedAttention)
		if m.wrap != nil {
			m.setFocus(false) // jump to the agent so you can answer right away
		}
	case ipc.Done:
		m.agentKind, m.agentMsg = ipc.Done, ev.Message
		if prev != ipc.Done && m.pause == notPaused {
			m.world.Celebrate()
			m.pauseAt = m.now + donePauseIn
		} else if m.pause != pausedManual {
			m.setPause(pausedDone)
		}
		if m.wrap != nil {
			m.setFocus(false)
		}
	case ipc.Working:
		m.agentKind, m.agentMsg = ipc.Working, ""
		m.pauseAt = 0
		if m.pause == pausedAttention || m.pause == pausedDone || m.pause == pausedWaiting {
			m.setPause(notPaused)
			if m.wrap != nil {
				m.setFocus(true) // back to the game while the agent works
			}
		}
	case ipc.Ended:
		m.agentKind, m.agentMsg = ipc.Ended, ""
	}
}

func (m *Model) setPause(p pauseReason) {
	m.pause = p
	if p != notPaused {
		m.in.ClearKeys()
	}
}

func (m *Model) setFocus(game bool) {
	if m.wrap == nil || m.focusGame == game {
		return
	}
	m.focusGame = game
	m.in.ClearKeys()
	m.wrap.focus(!game)
	if game && m.pause == pausedWaiting {
		m.setPause(notPaused) // jumped to the game before giving the agent work: just play
	}
}

func (m *Model) onKeyPress(k tea.KeyPressMsg) tea.Cmd {
	s := k.String()

	if m.wrap != nil && s == switchKey {
		m.setFocus(!m.focusGame)
		return nil
	}
	if m.agentFocused() {
		m.wrap.sendKey(k)
		return nil
	}
	if m.wrap == nil && (s == "ctrl+c" || s == "q") {
		return tea.Quit
	}
	if m.wrap != nil && s == "ctrl+c" {
		m.flash("ctrl+] switches to " + m.agentName + " (ctrl+c works there)")
		return nil
	}

	name := keyName(tea.Key(k))
	if !k.IsRepeat {
		m.trackEasterEggs(name)
	}

	switch s {
	case "p", "esc":
		if m.pause == notPaused {
			m.setPause(pausedManual)
		} else if m.pause == pausedManual {
			m.setPause(notPaused)
		}
		return nil
	case "enter", "space":
		// Dismiss a "your agent is done / needs you" card and keep playing.
		if m.pause == pausedDone || m.pause == pausedAttention {
			m.setPause(notPaused)
			return nil
		}
	}
	if m.pause != notPaused {
		return nil
	}
	switch name {
	case "e", "b":
		m.in.QueueBomb()
		return nil
	}
	m.in.Press(name, m.now, k.IsRepeat)
	return nil
}

func (m *Model) onMouse(msg tea.MouseMsg) {
	mo := msg.Mouse()
	inGame := mo.X >= m.lay.gameX && mo.Y >= m.lay.gameY &&
		mo.X < m.lay.gameX+m.lay.gameW && mo.Y < m.lay.gameY+m.lay.gameH

	if m.wrap != nil {
		if _, click := msg.(tea.MouseClickMsg); click {
			m.setFocus(inGame) // click a pane to focus it
		}
		if !inGame {
			if m.wrap.started() {
				m.wrap.sendMouse(msg, m.lay.agentX, m.lay.agentY)
			}
			return
		}
	}
	if !m.focusGame {
		return
	}
	// Cell -> arena pixel. Row 0 of the pane is the HUD.
	px := float64(mo.X-m.lay.gameX) + 0.5
	py := float64((mo.Y-m.lay.gameY-1)*2) + 1
	switch msg.(type) {
	case tea.MouseMotionMsg:
		m.in.MouseMove(px, py, m.now)
	case tea.MouseClickMsg:
		m.in.MouseMove(px, py, m.now)
		switch mo.Button {
		case tea.MouseLeft:
			m.in.MouseButton(true, m.now)
		case tea.MouseRight:
			m.in.QueueBomb()
		}
	case tea.MouseReleaseMsg:
		if mo.Button == tea.MouseLeft || mo.Button == tea.MouseNone {
			m.in.MouseButton(false, m.now)
		}
	}
}

// keyName normalizes a key to the names the input layer uses.
func keyName(k tea.Key) string {
	switch k.Code {
	case tea.KeyUp:
		return "up"
	case tea.KeyDown:
		return "down"
	case tea.KeyLeft:
		return "left"
	case tea.KeyRight:
		return "right"
	case tea.KeySpace:
		return "space"
	}
	if k.Code > 0 && k.Code < unicode.MaxRune && unicode.IsLetter(k.Code) {
		return string(unicode.ToLower(k.Code))
	}
	return tea.KeyPressMsg(k).String()
}

var konami = []string{"up", "up", "down", "down", "left", "right", "left", "right", "b", "a"}

func (m *Model) trackEasterEggs(name string) {
	m.recentKeys = append(m.recentKeys, name)
	if len(m.recentKeys) > 10 {
		m.recentKeys = m.recentKeys[len(m.recentKeys)-10:]
	}
	if len(m.recentKeys) == 10 && strings.Join(m.recentKeys, ",") == strings.Join(konami, ",") {
		m.world.TogglePartyMode()
		m.recentKeys = nil
	}
	tail := strings.Join(m.recentKeys, "")
	if strings.HasSuffix(tail, "sudo") {
		m.flash("nice try. blobs don't do sudo")
		m.recentKeys = nil
	}
}

func (m *Model) flash(s string) {
	m.toast = s
	m.toastUntil = m.now + 2.5
}

// View draws one frame.
func (m *Model) View() tea.View {
	if m.cols == 0 || m.rows == 0 {
		return tea.NewView("")
	}
	ui := m.ui()
	gameLines := m.rend.Render(m.world, ui, m.lay.gameW, m.lay.gameH)

	var b strings.Builder
	var v tea.View
	if m.wrap == nil {
		b.WriteString(strings.Join(gameLines, "\n"))
	} else {
		var agent []string
		if m.wrap.started() {
			agent = m.wrap.lines()
		} else {
			agent = blankLines(m.lay.agentW, m.lay.agentH)
		}
		div := "\x1b[38;2;58;50;88m"
		if m.focusGame {
			div = "\x1b[38;2;138;180;232m"
		}
		if m.lay.side {
			for y := 0; y < m.rows; y++ {
				if y > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(lineAt(agent, y, m.lay.agentW))
				b.WriteString(div + "│\x1b[0m")
				b.WriteString(lineAt(gameLines, y, m.lay.gameW))
			}
		} else {
			for y := 0; y < m.lay.agentH; y++ {
				b.WriteString(lineAt(agent, y, m.lay.agentW))
				b.WriteByte('\n')
			}
			b.WriteString(div + strings.Repeat("─", m.cols) + "\x1b[0m")
			for y := 0; y < m.lay.gameH; y++ {
				b.WriteByte('\n')
				b.WriteString(lineAt(gameLines, y, m.lay.gameW))
			}
		}
		if m.agentFocused() {
			if x, y, ok := m.wrap.cursor(); ok {
				v.Cursor = tea.NewCursor(m.lay.agentX+x, m.lay.agentY+y)
			}
		}
	}
	v.Content = b.String()
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	v.ReportFocus = true
	v.KeyboardEnhancements.ReportEventTypes = true
	v.WindowTitle = m.title()
	return v
}

func (m *Model) title() string {
	name := firstNonEmpty(m.agentName, "agent")
	switch m.agentKind {
	case ipc.Attention:
		return "◆ " + name + " needs you · blob"
	case ipc.Done:
		return "✓ " + name + " done · blob"
	}
	return "blob"
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (m *Model) ui() render.UI {
	name := firstNonEmpty(m.agentName, "agent")
	ui := render.UI{Time: m.now, Dim: m.wrap != nil && !m.focusGame && m.pause == notPaused}

	switch m.agentKind {
	case ipc.Working:
		ui.StatusIcon = spinner[int(m.now*10)%len(spinner)]
		ui.StatusText, ui.StatusColor = name+" working", game.ColPeriwinkle
	case ipc.Attention:
		ui.StatusIcon, ui.StatusText, ui.StatusColor = "◆", name+" needs you", game.ColCoral
	case ipc.Done:
		ui.StatusIcon, ui.StatusText, ui.StatusColor = "✓", name+" done", game.ColSage
	default:
		if m.wrap == nil {
			ui.StatusIcon, ui.StatusText, ui.StatusColor = "○", "no agent yet", game.Hex(0x6E6688)
		}
	}

	switch m.pause {
	case pausedAttention:
		ui.Paused, ui.PauseAccent = true, game.ColCoral
		ui.PauseTitle = "◆ " + name + " needs you"
		ui.PauseBody = firstNonEmpty(m.agentMsg, "it's waiting on a decision")
		ui.PauseHint = "answer it · the game waits for you"
		if m.wrap == nil {
			ui.PauseHint = "go answer it · enter keeps playing"
		}
	case pausedDone:
		ui.Paused, ui.PauseAccent = true, game.ColSage
		ui.PauseTitle = "✓ " + name + " is done"
		ui.PauseBody = firstNonEmpty(m.agentMsg, "your turn")
		ui.PauseHint = "ctrl+] keeps playing"
		if m.wrap == nil {
			ui.PauseHint = "enter keeps playing"
		}
	case pausedManual:
		ui.Paused, ui.PauseAccent = true, game.ColPeriwinkle
		ui.PauseTitle, ui.PauseBody, ui.PauseHint = "paused", "the blobs are napping", "p to resume"
	case pausedWaiting:
		ui.Paused, ui.PauseAccent = true, game.ColMustard
		ui.PauseTitle = "give " + name + " a task"
		ui.PauseBody = "the match starts when it gets to work"
		ui.PauseHint = "ctrl+] to play right now"
	}

	hints := "wasd move · mouse/arrows aim+fire · space fire · e bomb · p pause"
	if m.wrap != nil {
		hints += " · ctrl+] " + name
	} else {
		hints += " · q quit"
	}
	if !m.precise && m.now < 25 {
		hints = "tip: kitty/ghostty/wezterm = smoother keys · " + hints
	}
	if m.now < m.toastUntil {
		hints = m.toast
	}
	ui.Hints = hints
	return ui
}

func lineAt(lines []string, y, w int) string {
	if y < len(lines) {
		return lines[y]
	}
	return strings.Repeat(" ", max(0, w))
}

func blankLines(w, h int) []string {
	out := make([]string, h)
	for i := range out {
		out[i] = strings.Repeat(" ", max(0, w))
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Exit prints a friendly line after the program ends (used by main).
func (m *Model) Exit() {
	if m.wrap != nil {
		m.wrap.kill()
	}
	if m.exitErr != nil {
		fmt.Fprintln(os.Stderr, "blob:", m.exitErr)
	}
}
