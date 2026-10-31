package hook

import (
	"testing"

	"github.com/abhay/blob/internal/ipc"
)

func TestTranslate(t *testing.T) {
	cases := []struct {
		name, agent, arg, payload string
		want                      ipc.Kind
		send                      bool
		stdout                    string
	}{
		{"claude prompt", "claude", "", `{"hook_event_name":"UserPromptSubmit"}`, ipc.Working, true, ""},
		{"claude permission", "claude", "", `{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`, ipc.Attention, true, ""},
		{"claude notify permission", "claude", "", `{"hook_event_name":"Notification","message":"Claude needs your permission to use Bash"}`, ipc.Attention, true, ""},
		{"claude idle", "claude", "", `{"hook_event_name":"Notification","notification_type":"idle_prompt","message":"Claude is waiting for your input"}`, ipc.Done, true, ""},
		{"claude stop", "claude", "", `{"hook_event_name":"Stop"}`, ipc.Done, true, ""},
		{"claude unknown", "claude", "", `{"hook_event_name":"PreCompact"}`, "", false, ""},
		{"codex legacy notify", "codex", "", `{"type":"agent-turn-complete","last-assistant-message":"All tests pass."}`, ipc.Done, true, ""},
		{"codex permission", "codex", "", `{"hook_event_name":"PermissionRequest"}`, ipc.Attention, true, ""},
		{"gemini permission", "gemini", "", `{"hook_event_name":"Notification","notification_type":"ToolPermission"}`, ipc.Attention, true, ""},
		{"gemini done", "gemini", "", `{"hook_event_name":"AfterAgent"}`, ipc.Done, true, ""},
		{"agy stop (no name in payload)", "agy", "Stop", `{"fullyIdle":true}`, ipc.Done, true, "{}"},
		{"agy run_command", "agy", "PreToolUse", `{"toolCall":{"name":"run_command"}}`, ipc.Attention, true, `{"decision":"ask"}`},
		{"agy read", "agy", "PreToolUse", `{"toolCall":{"name":"view_file"}}`, ipc.Working, true, `{"decision":"ask"}`},
		{"empty payload", "claude", "", ``, "", false, ""},
	}
	for _, c := range cases {
		r := Translate(c.agent, c.arg, []byte(c.payload))
		if r.Send != c.send || r.Event.Kind != c.want || r.Stdout != c.stdout {
			t.Errorf("%s: got send=%v kind=%q stdout=%q, want send=%v kind=%q stdout=%q",
				c.name, r.Send, r.Event.Kind, r.Stdout, c.send, c.want, c.stdout)
		}
	}
}
