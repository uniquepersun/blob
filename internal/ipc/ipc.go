// Package ipc is the tiny protocol between agent hooks and the game: one
// JSON object per line over a Unix socket. Hooks connect, write one line,
// and leave. If no game is listening, sending fails fast and silently, so a
// hook can never slow an agent down.
package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Kind is what the agent is up to.
type Kind string

const (
	Working   Kind = "working"   // thinking or running tools: play on
	Attention Kind = "attention" // needs approval or input: freeze the game
	Done      Kind = "done"      // finished its turn: your move
	Ended     Kind = "ended"     // session closed
)

// Event is one message from a hook to the game.
type Event struct {
	Agent   string `json:"agent"`
	Kind    Kind   `json:"kind"`
	Message string `json:"message,omitempty"`
	Tool    string `json:"tool,omitempty"`
}

// EnvSocket overrides the socket path. The wrapper (`blob claude`) sets it
// for the agent it launches, so that agent's hooks reach that game.
const EnvSocket = "BLOB_SOCKET"

// SocketPath returns where the game listens.
func SocketPath() string {
	if p := os.Getenv(EnvSocket); p != "" {
		return p
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, fmt.Sprintf("blob-%d.sock", os.Getuid()))
}

// ErrInUse means another game is already listening on the socket.
var ErrInUse = errors.New("another blob game is already listening on this socket")

// Listen serves events on path and calls handle for each one. The returned
// function closes the listener and removes the socket file.
func Listen(path string, handle func(Event)) (func(), error) {
	if _, err := os.Stat(path); err == nil {
		if c, err := net.DialTimeout("unix", path, 100*time.Millisecond); err == nil {
			c.Close()
			return nil, ErrInUse
		}
		_ = os.Remove(path) // stale socket from a crashed game
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
				sc := bufio.NewScanner(c)
				sc.Buffer(make([]byte, 64*1024), 1<<20)
				for sc.Scan() {
					var ev Event
					if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Kind != "" {
						handle(ev)
					}
				}
			}(conn)
		}
	}()
	return func() {
		ln.Close()
		_ = os.Remove(path)
	}, nil
}

// Send delivers one event. It gives up after a short timeout.
func Send(path string, ev Event) error {
	c, err := net.DialTimeout("unix", path, 150*time.Millisecond)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetWriteDeadline(time.Now().Add(150 * time.Millisecond))
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = c.Write(append(b, '\n'))
	return err
}
