// Package app is the Bubble Tea program: the game on its own, or an agent
// running in a pseudo-terminal with the game tucked away until you open it.
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

	"github.com/uniquepersun/blob/internal/game"
	"github.com/uniquepersun/blob/internal/input"
	"github.com/uniquepersun/blob/internal/ipc"
	"github.com/uniquepersun/blob/internal/render"
)

const (
	frameTime   = time.Second / 30
	scanEvery   = 0.3 // seconds between prompt scans of the agent screen
	donePauseIn = 1.3 // let the celebration confetti fly before freezing
	switchKey   = "ctrl+]"
	minSideCols = 110 // side layout needs this many columns, else it stacks
	nudgeFor    = 5.0 // seconds the corner chip pulses when the agent starts
	defGamePct  = 60  // game panel height, percent of the terminal
	minAgentH   = 8   // the agent keeps at least this many rows
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
)

// Config is how main starts the app.
type Config struct {
	Socket string
	Agent  string   // wrapper mode: display name ("claude")
	Argv   []string // wrapper mode: command to run
	Bots   int

	Side    bool // wrapper: open the game on the right instead of below
	GamePct int  // wrapper: game panel height in percent (stacked layout)
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
	open       bool    // wrapper: game panel visible
	reopen     bool    // wrapper: reopen the game when the agent resumes
	hideAt     float64 // wrapper: close the panel after the done confetti
	nudgeUntil float64 // wrapper: corner chip pulses until then
	chipX      int     // wrapper: corner chip on row 0, for clicks
	chipW      int
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
	if cfg.GamePct < 20 || cfg.GamePct > 85 {
		cfg.GamePct = defGamePct
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
		m.focusGame = false // the agent gets the whole screen until you open the game
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
	switch {
	case m.wrap == nil:
		l.gameW, l.gameH = m.cols, m.rows
	case !m.open:
		l.agentW, l.agentH = m.cols, m.rows
	case m.cfg.Side && m.cols >= minSideCols:
		l.side = true
		l.agentW = m.cols * 56 / 100
		l.agentH = m.rows
		l.gameX = l.agentW + 1
		l.gameW = m.cols - l.gameX
		l.gameH = m.rows
	default:
		l.agentW = m.cols
		l.agentH = max(min(minAgentH, m.rows/2), m.rows-1-m.rows*m.cfg.GamePct/100)
		l.gameY = l.agentH + 1
		l.gameW = m.cols
		l.gameH = max(1, m.rows-l.gameY)
	}
	m.lay = l
	if l.gameW > 0 && l.gameH > 0 {
		aw, ah := render.ArenaSize(l.gameW, l.gameH)
		m.world.Resize(float64(aw), float64(ah))
	}
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
	if m.hideAt > 0 && m.now >= m.hideAt {
		m.hideAt = 0
		m.showGame(false)
	}

	if m.wrap != nil && m.now >= m.nextScan {
		m.nextScan = m.now + scanEvery
		m.scanPrompt()
	}

	if m.pause != notPaused || (m.wrap != nil && !m.open) {
		m.acc = 0 // hidden counts as paused
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
	if m.wrap != nil {
		m.onAgentWrapped(ev)
		return
	}
	prev := m.agentKind
	switch ev.Kind {
	case ipc.Attention:
		m.agentKind, m.agentMsg = ipc.Attention, ev.Message
		m.pauseAt = 0
		m.setPause(pausedAttention)
	case ipc.Done:
		m.agentKind, m.agentMsg = ipc.Done, ev.Message
		if prev != ipc.Done && m.pause == notPaused {
			m.world.Celebrate()
			m.pauseAt = m.now + donePauseIn
		} else if m.pause != pausedManual {
			m.setPause(pausedDone)
		}
	case ipc.Working:
		m.agentKind, m.agentMsg = ipc.Working, ""
		m.pauseAt = 0
		if m.pause == pausedAttention || m.pause == pausedDone {
			m.setPause(notPaused)
		}
	case ipc.Ended:
		m.agentKind, m.agentMsg = ipc.Ended, ""
	}
}

// onAgentWrapped: the agent owns the screen whenever it needs you; the game
// only opens when you ask for it, or comes back if a question interrupted it.
func (m *Model) onAgentWrapped(ev ipc.Event) {
	prev := m.agentKind
	switch ev.Kind {
	case ipc.Attention:
		m.agentKind, m.agentMsg = ipc.Attention, ev.Message
		m.hideAt, m.nudgeUntil = 0, 0
		if m.open {
			m.showGame(false)
			m.reopen = true // you were mid-fight: come back after answering
		}
	case ipc.Done:
		m.agentKind, m.agentMsg = ipc.Done, ev.Message
		m.reopen, m.nudgeUntil = false, 0
		if m.open && prev != ipc.Done && m.hideAt == 0 {
			m.world.Celebrate()
			m.hideAt = m.now + donePauseIn
			m.setFocus(false) // keys go to the agent while the confetti flies
		} else if m.open && m.hideAt == 0 {
			m.showGame(false)
		}
	case ipc.Working:
		m.agentKind, m.agentMsg = ipc.Working, ""
		if prev == ipc.Working {
			return // arrives on every tool call; only the transition matters
		}
		switch {
		case m.reopen:
			m.showGame(true)
		case !m.open:
			m.nudgeUntil = m.now + nudgeFor
		}
	case ipc.Ended:
		m.agentKind, m.agentMsg = ipc.Ended, ""
	}
}

// showGame opens or closes the game panel. The agent's pty is resized, so
// it redraws to fit the space it has.
func (m *Model) showGame(on bool) {
	if m.wrap == nil || m.open == on {
		return
	}
	m.open = on
	m.reopen, m.hideAt, m.nudgeUntil = false, 0, 0
	m.relayout()
	m.setFocus(on)
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
	if game && !m.open {
		m.showGame(true) // calls back here with the panel open
		return
	}
	m.focusGame = game
	m.in.ClearKeys()
	m.wrap.focus(!game)
}

func (m *Model) onKeyPress(k tea.KeyPressMsg) tea.Cmd {
	s := k.String()

	if m.wrap != nil && s == switchKey {
		switch {
		case !m.open:
			m.showGame(true)
		case m.focusGame:
			m.showGame(false)
		default:
			m.setFocus(true) // panel open, agent focused: back to the game
		}
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
		m.flash("ctrl+] hides the game (ctrl+c works in " + m.agentName + ")")
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
		_, click := msg.(tea.MouseClickMsg)
		if click && !m.open && m.chipW > 0 && mo.Y == 0 && mo.X >= m.chipX && mo.X < m.chipX+m.chipW {
			m.showGame(true) // clicked the corner chip
			return
		}
		if click {
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
	var gameLines []string
	if m.lay.gameW > 0 && m.lay.gameH > 0 {
		gameLines = m.rend.Render(m.world, m.ui(), m.lay.gameW, m.lay.gameH)
	}

	var b strings.Builder
	var v tea.View
	m.chipW = 0
	if m.wrap == nil {
		b.WriteString(strings.Join(gameLines, "\n"))
	} else {
		var agent []string
		if m.wrap.started() {
			agent = m.wrap.lines()
		} else {
			agent = blankLines(m.lay.agentW, m.lay.agentH)
		}
		if !m.open {
			m.drawChip(agent)
			b.WriteString(strings.Join(agent, "\n"))
			if m.agentFocused() {
				if x, y, ok := m.wrap.cursor(); ok {
					v.Cursor = tea.NewCursor(x, y)
				}
			}
			return m.finish(v, b.String())
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
	return m.finish(v, b.String())
}

func (m *Model) finish(v tea.View, content string) tea.View {
	v.Content = content
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	v.ReportFocus = true
	v.KeyboardEnhancements.ReportEventTypes = true
	v.WindowTitle = m.title()
	return v
}

// drawChip puts the small "ctrl+] blob" chip in the agent's top-right
// corner while the game is hidden. It grows and pulses for a few seconds
// when the agent starts working, to nudge you toward a round.
func (m *Model) drawChip(agent []string) {
	if len(agent) == 0 || !m.wrap.started() {
		return
	}
	name := firstNonEmpty(m.agentName, "agent")
	icon, fg := "●", game.Hex(0x8C84A8)
	switch m.agentKind {
	case ipc.Working:
		icon, fg = spinner[int(m.now*10)%len(spinner)], game.ColPeriwinkle
	case ipc.Attention:
		icon, fg = "◆", game.ColCoral
	case ipc.Done:
		icon, fg = "✓", game.ColSage
	}
	bg := game.Hex(0x221D33)
	text := " " + icon + " " + switchKey + " blob "
	bold := false
	if m.now < m.nudgeUntil {
		text = " " + icon + " " + name + "'s working · " + switchKey + " play blob "
		pulse := 0.5 + 0.5*math.Sin(m.now*2*math.Pi/1.2)
		bg = render.Mix(game.Hex(0x221D33), game.Hex(0x3C3466), pulse)
		fg, bold = game.ColMustard, true
	}
	w := render.Width(text)
	cols := m.lay.agentW
	if cols < w+12 {
		return // too narrow to spare the corner
	}
	x := cols - w - 1
	seq := "\x1b[0m\x1b[48;2;" + rgb(bg) + "m\x1b[38;2;" + rgb(fg) + "m"
	if bold {
		seq += "\x1b[1m"
	}
	agent[0] = m.wrap.rowRange(0, 0, x) + seq + text + "\x1b[0m" + m.wrap.rowRange(0, x+w, cols)
	m.chipX, m.chipW = x, w
}

func rgb(c game.RGB) string { return fmt.Sprintf("%d;%d;%d", c.R, c.G, c.B) }

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
	ui := render.UI{Time: m.now, Dim: m.wrap != nil && !m.focusGame && m.pause == notPaused && m.hideAt == 0}

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
	}

	hints := "wasd move · mouse/arrows aim+fire · space fire · e bomb · p pause"
	if m.wrap != nil {
		hints += " · ctrl+] hide"
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
