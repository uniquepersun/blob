// Package hook translates each agent's hook payload into a game event.
//
// Every agent CLI names its lifecycle events differently and sends a
// different JSON shape on stdin. This file is the only place that knows
// those differences; the game only ever sees ipc.Event.
package hook

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/uniquepersun/blob/internal/ipc"
)

// Result is what the hook process should do.
type Result struct {
	Event  ipc.Event
	Send   bool   // false: nothing worth telling the game
	Stdout string // some agents require a JSON reply on stdout
}

// payload is the union of fields we read from any agent.
type payload struct {
	HookEventName    string          `json:"hook_event_name"`
	ToolName         string          `json:"tool_name"`
	Message          string          `json:"message"`
	NotificationType string          `json:"notification_type"`
	Type             string          `json:"type"`                   // codex legacy notify
	LastMessage      string          `json:"last-assistant-message"` // codex legacy notify
	ToolCall         *agyToolCall    `json:"toolCall"`               // agy
	Details          json.RawMessage `json:"details"`
}

type agyToolCall struct {
	Name string `json:"name"`
}

// Agents lists the agents we have adapters for.
var Agents = []string{"claude", "codex", "gemini", "agy"}

// Translate maps (agent, event name from argv, raw payload) to a Result.
// eventArg is only needed for agents that don't put the event name in the
// payload (agy).
func Translate(agent, eventArg string, raw []byte) Result {
	var p payload
	_ = json.Unmarshal(raw, &p) // tolerate empty or odd payloads
	name := p.HookEventName
	if name == "" {
		name = eventArg
	}

	r := Result{Event: ipc.Event{Agent: agent, Tool: p.ToolName}}
	set := func(k ipc.Kind, msg string) {
		r.Event.Kind, r.Event.Message, r.Send = k, msg, true
	}

	switch agent {
	case "claude", "codex":
		switch name {
		case "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "SubagentStart":
			set(ipc.Working, "")
		case "PermissionRequest":
			set(ipc.Attention, permissionMsg(p.ToolName))
		case "Notification":
			switch {
			case p.NotificationType == "idle_prompt":
				set(ipc.Done, firstNonEmpty(p.Message, "waiting for your input"))
			default:
				set(ipc.Attention, firstNonEmpty(p.Message, "needs your attention"))
			}
		case "Stop":
			set(ipc.Done, "")
		case "SessionEnd":
			set(ipc.Ended, "")
		}
		// Codex's older `notify = [...]` hook passes {"type": "agent-turn-complete", ...}.
		if !r.Send && agent == "codex" {
			switch {
			case p.Type == "agent-turn-complete":
				set(ipc.Done, oneLine(p.LastMessage))
			case strings.Contains(p.Type, "approval"):
				set(ipc.Attention, "needs your approval")
			}
		}

	case "gemini":
		switch name {
		case "BeforeAgent", "BeforeTool", "AfterTool", "BeforeModel":
			set(ipc.Working, "")
		case "Notification":
			if p.NotificationType == "ToolPermission" {
				set(ipc.Attention, firstNonEmpty(p.Message, "wants to run a tool"))
			} else {
				set(ipc.Attention, firstNonEmpty(p.Message, "needs your attention"))
			}
		case "AfterAgent":
			set(ipc.Done, "")
		case "SessionEnd":
			set(ipc.Ended, "")
		}

	case "agy":
		// agy has no permission/notification event. Its hooks are synchronous
		// and it fails closed on a malformed reply, so we always answer.
		r.Stdout = "{}"
		switch name {
		case "PreInvocation", "PostInvocation", "PostToolUse":
			set(ipc.Working, "")
		case "PreToolUse":
			// Opt-in only (see `blob install agy --permission-hint`). With
			// agy's default request-review policy, a run_command is about to
			// prompt. "ask" keeps agy's normal behavior; we never "allow".
			r.Stdout = `{"decision":"ask"}`
			tool := ""
			if p.ToolCall != nil {
				tool = p.ToolCall.Name
			}
			r.Event.Tool = tool
			if tool == "run_command" {
				set(ipc.Attention, "probably wants to run a command")
			} else {
				set(ipc.Working, "")
			}
		case "Stop":
			set(ipc.Done, "")
		}
	}
	return r
}

func permissionMsg(tool string) string {
	if tool == "" {
		return "needs your permission"
	}
	return fmt.Sprintf("wants to use %s", tool)
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return oneLine(a)
	}
	return b
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 60 {
		s = string(r[:59]) + "…"
	}
	return s
}
