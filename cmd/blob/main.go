// Command blob is a cozy blob shooter that lives next to your coding agent.
//
//	blob                      play on your own (hooks from any agent pause it)
//	blob claude [args...]     run Claude Code with the game a keypress away
//	blob codex|gemini|agy     same for other agents
//	blob run -- <cmd...>      same for any CLI
//	blob install <agent>      add blob's hooks to an agent's config
//	blob uninstall <agent>    remove them
//	blob hook <agent> [event] (called by the agent, not by you)
//	blob send <kind> [msg]    fake an agent event, for testing
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/abhay/blob/internal/app"
	"github.com/abhay/blob/internal/hook"
	"github.com/abhay/blob/internal/install"
	"github.com/abhay/blob/internal/ipc"
)

const version = "0.1.0"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		os.Exit(play(app.Config{Socket: ipc.SocketPath()}))
	}
	switch args[0] {
	case "hook":
		runHook(args[1:]) // must stay fast and always exit 0
		return
	case "play":
		os.Exit(play(app.Config{Socket: ipc.SocketPath()}))
	case "run":
		rest := args[1:]
		if len(rest) > 0 && rest[0] == "--" {
			rest = rest[1:]
		}
		if len(rest) == 0 {
			die("usage: blob run -- <command> [args...]")
		}
		os.Exit(wrap(filepath.Base(rest[0]), rest))
	case "install", "uninstall":
		os.Exit(runInstall(args[0] == "uninstall", args[1:]))
	case "send":
		os.Exit(runSend(args[1:]))
	case "version", "--version", "-v":
		fmt.Println("blob", version)
		return
	case "help", "--help", "-h":
		fmt.Print(usage)
		return
	}
	for _, a := range hook.Agents {
		if args[0] == a {
			os.Exit(wrap(a, args))
		}
	}
	fmt.Fprintf(os.Stderr, "blob: unknown command %q\n\n%s", args[0], usage)
	os.Exit(2)
}

const usage = `blob: a cozy blob shooter for while your agent works

  blob                       play; any agent with blob hooks pauses it
  blob claude [args...]      Claude Code, with the game a keypress away
  blob codex | gemini | agy  same for other agents
  blob run -- <cmd...>       same for any CLI
  blob install <agent>       add hooks (claude, codex, gemini, agy)
       --project             write the project config instead of the global one
       --agy-permissions     agy only: also watch run_command (see README)
  blob uninstall <agent>     remove hooks
  blob send <working|attention|done> [message]   fake an event (testing)

in game: wasd move · mouse or arrows aim+fire · space fire · e bomb
         p pause · ctrl+] show/hide the game (wrapper)

wrapper layout (environment):
  BLOB_GAME_PCT=60           game panel height in percent of the terminal
  BLOB_LAYOUT=side           open the game on the right instead of below
`

func play(cfg app.Config) int {
	m := app.New(cfg)
	p := tea.NewProgram(m)
	m.SetSend(p.Send)

	closeSock, err := ipc.Listen(cfg.Socket, func(ev ipc.Event) { p.Send(app.AgentEvent(ev)) })
	if err != nil {
		if err == ipc.ErrInUse && len(cfg.Argv) == 0 {
			die("a blob game is already running (socket %s)", cfg.Socket)
		}
		fmt.Fprintf(os.Stderr, "blob: hooks disabled, could not listen on %s: %v\n", cfg.Socket, err)
		time.Sleep(1200 * time.Millisecond)
	} else {
		defer closeSock()
	}

	_, runErr := p.Run()
	m.Exit()
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "blob:", runErr)
		return 1
	}
	if m.Err() != nil {
		return 1
	}
	return 0
}

func wrap(name string, argv []string) int {
	// Each wrapped session gets its own socket; the agent inherits
	// BLOB_SOCKET, so its hooks reach this window and not some other game.
	sock := filepath.Join(filepath.Dir(ipc.SocketPath()), fmt.Sprintf("blob-%d-%d.sock", os.Getuid(), os.Getpid()))
	pct, _ := strconv.Atoi(os.Getenv("BLOB_GAME_PCT"))
	return play(app.Config{
		Socket:  sock,
		Agent:   name,
		Argv:    argv,
		Side:    os.Getenv("BLOB_LAYOUT") == "side",
		GamePct: pct,
	})
}

// runHook is what agents execute. It reads the hook payload, forwards one
// event, prints any reply the agent needs, and never fails.
func runHook(args []string) {
	defer func() { _ = recover() }()
	if len(args) == 0 {
		return
	}
	agent := args[0]
	eventArg := ""
	var raw []byte
	for _, a := range args[1:] {
		if strings.HasPrefix(strings.TrimSpace(a), "{") {
			raw = []byte(a) // codex legacy notify passes JSON as an argument
		} else if eventArg == "" {
			eventArg = a
		}
	}
	if raw == nil {
		raw = readStdin(500 * time.Millisecond)
	}
	r := hook.Translate(agent, eventArg, raw)
	if r.Stdout != "" {
		fmt.Println(r.Stdout)
	}
	if r.Send {
		_ = ipc.Send(ipc.SocketPath(), r.Event)
	}
}

func readStdin(timeout time.Duration) []byte {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice != 0 {
		return nil // a terminal, not a pipe: nothing to read
	}
	ch := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
		ch <- b
	}()
	select {
	case b := <-ch:
		return b
	case <-time.After(timeout):
		return nil
	}
}

func runInstall(uninstall bool, args []string) int {
	o := install.Options{Uninstall: uninstall}
	for _, a := range args {
		switch a {
		case "--project":
			o.Project = true
		case "--agy-permissions":
			o.AgyPermissions = true
		default:
			if strings.HasPrefix(a, "-") {
				die("unknown flag %s", a)
			}
			o.Agent = a
		}
	}
	if o.Agent == "" {
		die("usage: blob %s <claude|codex|gemini|agy> [--project]", map[bool]string{true: "uninstall", false: "install"}[uninstall])
	}
	bin, err := os.Executable()
	if err != nil {
		die("cannot find my own path: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}
	o.Bin = bin
	rep, err := install.Run(o)
	if err != nil {
		die("%v", err)
	}
	verb := "installed hooks in"
	if uninstall {
		verb = "removed hooks from"
	}
	fmt.Printf("blob: %s %s\n", verb, rep.Path)
	for _, n := range rep.Notes {
		fmt.Println("  " + n)
	}
	return 0
}

func runSend(args []string) int {
	if len(args) == 0 {
		die("usage: blob send <working|attention|done|ended> [message]")
	}
	ev := ipc.Event{Agent: "test", Kind: ipc.Kind(args[0]), Message: strings.Join(args[1:], " ")}
	if err := ipc.Send(ipc.SocketPath(), ev); err != nil {
		die("no game listening on %s (%v)", ipc.SocketPath(), err)
	}
	return 0
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "blob: "+format+"\n", a...)
	os.Exit(1)
}
