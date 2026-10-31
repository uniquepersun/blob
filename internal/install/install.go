// Package install adds (and removes) blob's hooks in each agent's config.
//
// It only ever touches entries whose command runs `blob ... hook`, keeps
// everything else in the file, and writes a timestamped backup first.
package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Options controls an install.
type Options struct {
	Agent          string
	Project        bool   // write the per-project file instead of the global one
	Bin            string // absolute path of the blob binary
	AgyPermissions bool   // also register agy's PreToolUse (see README)
	Uninstall      bool
}

// Report says what happened, for printing.
type Report struct {
	Path  string
	Notes []string
}

// Run installs or uninstalls hooks for one agent.
func Run(o Options) (Report, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Report{}, err
	}
	cmd := func(rest string) string { return shellQuote(o.Bin) + " hook " + rest }

	switch o.Agent {
	case "claude":
		path := filepath.Join(home, ".claude", "settings.json")
		if o.Project {
			path = filepath.Join(".claude", "settings.json")
		}
		events := map[string]bool{ // event -> takes a tool matcher
			"UserPromptSubmit": false, "PreToolUse": true, "PostToolUse": true,
			"PermissionRequest": true, "Notification": false, "Stop": false, "SessionEnd": false,
		}
		err := editJSON(path, o.Uninstall, func(root map[string]any) {
			mergeGrouped(root, events, cmd("claude"), "*", o.Uninstall, false)
		})
		rep := Report{Path: path}
		if !o.Uninstall {
			rep.Notes = append(rep.Notes, "Restart Claude Code (or open /hooks) so it reloads hooks.")
		}
		return rep, err

	case "codex":
		path := filepath.Join(home, ".codex", "hooks.json")
		if o.Project {
			path = filepath.Join(".codex", "hooks.json")
		}
		events := map[string]bool{
			"UserPromptSubmit": false, "PreToolUse": true, "PostToolUse": true,
			"PermissionRequest": true, "Stop": false,
		}
		err := editJSON(path, o.Uninstall, func(root map[string]any) {
			mergeGrouped(root, events, cmd("codex"), "*", o.Uninstall, false)
		})
		rep := Report{Path: path}
		if !o.Uninstall {
			rep.Notes = append(rep.Notes,
				"Open Codex, run /hooks and trust the blob hooks (Codex asks once).",
				"Older Codex without hooks.json: add this as the first line of ~/.codex/config.toml instead:",
				fmt.Sprintf("  notify = [%q, \"hook\", \"codex\"]", o.Bin))
		}
		return rep, err

	case "gemini":
		path := filepath.Join(home, ".gemini", "settings.json")
		if o.Project {
			path = filepath.Join(".gemini", "settings.json")
		}
		events := map[string]bool{
			"BeforeAgent": false, "BeforeTool": true, "AfterTool": true,
			"Notification": false, "AfterAgent": false, "SessionEnd": false,
		}
		err := editJSON(path, o.Uninstall, func(root map[string]any) {
			mergeGrouped(root, events, cmd("gemini"), ".*", o.Uninstall, true)
		})
		return Report{Path: path}, err

	case "agy":
		path := filepath.Join(home, ".gemini", "config", "hooks.json")
		if o.Project {
			path = filepath.Join(".agents", "hooks.json")
		}
		err := editJSON(path, o.Uninstall, func(root map[string]any) {
			if o.Uninstall {
				delete(root, "blob")
				return
			}
			flat := func(ev string) []any {
				return []any{map[string]any{"type": "command", "command": cmd("agy " + ev), "timeout": 5}}
			}
			grouped := func(ev, matcher string) []any {
				return []any{map[string]any{"matcher": matcher, "hooks": []any{
					map[string]any{"type": "command", "command": cmd("agy " + ev), "timeout": 5},
				}}}
			}
			// agy wants PreInvocation/PostInvocation/Stop as flat handler lists
			// and the tool events grouped with a matcher; mixing them up makes
			// agy reject the whole file.
			entry := map[string]any{
				"enabled":       true,
				"PreInvocation": flat("PreInvocation"),
				"PostToolUse":   grouped("PostToolUse", "*"),
				"Stop":          flat("Stop"),
			}
			if o.AgyPermissions {
				entry["PreToolUse"] = grouped("PreToolUse", "run_command")
			}
			root["blob"] = entry
		})
		rep := Report{Path: path}
		if !o.Uninstall {
			rep.Notes = append(rep.Notes,
				"agy has no permission event. Run it through the wrapper (`blob agy`) so blob can",
				"spot approval prompts on screen, or reinstall with --agy-permissions (see README).")
		}
		return rep, err
	}
	return Report{}, fmt.Errorf("unknown agent %q (try: claude, codex, gemini, agy)", o.Agent)
}

// mergeGrouped handles the Claude-style shape shared by Claude Code, Codex
// and Gemini CLI: {"hooks": {"Event": [{"matcher": "...", "hooks": [{...}]}]}}.
func mergeGrouped(root map[string]any, events map[string]bool, command, matcher string, uninstall, named bool) {
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	for ev, takesMatcher := range events {
		groups, _ := hooks[ev].([]any)
		groups = removeOurs(groups)
		if !uninstall {
			h := map[string]any{"type": "command", "command": command, "timeout": 5}
			if named {
				h["name"] = "blob"
				h["timeout"] = 5000 // Gemini uses milliseconds
			}
			g := map[string]any{"hooks": []any{h}}
			if takesMatcher {
				g["matcher"] = matcher
			}
			groups = append(groups, g)
		}
		if len(groups) == 0 {
			delete(hooks, ev)
		} else {
			hooks[ev] = groups
		}
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	} else {
		root["hooks"] = hooks
	}
}

// removeOurs drops any handler that runs blob's hook command, and any group
// left empty by that.
func removeOurs(groups []any) []any {
	out := groups[:0:0]
	for _, g := range groups {
		gm, ok := g.(map[string]any)
		if !ok {
			out = append(out, g)
			continue
		}
		hs, _ := gm["hooks"].([]any)
		kept := []any{}
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			c, _ := hm["command"].(string)
			if isOurs(c) {
				continue
			}
			kept = append(kept, h)
		}
		if len(kept) == 0 && len(hs) > 0 {
			continue
		}
		gm["hooks"] = kept
		out = append(out, gm)
	}
	return out
}

func isOurs(cmd string) bool {
	return strings.Contains(cmd, "blob") && strings.Contains(cmd, " hook ")
}

func editJSON(path string, uninstall bool, edit func(map[string]any)) error {
	root := map[string]any{}
	data, err := os.ReadFile(path)
	if uninstall && errors.Is(err, os.ErrNotExist) {
		return nil // nothing to remove
	}
	switch {
	case err == nil && len(strings.TrimSpace(string(data))) > 0:
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("%s is not valid JSON, not touching it: %w", path, err)
		}
		backup := path + ".blob-backup-" + strconv.FormatInt(time.Now().Unix(), 10)
		if err := os.WriteFile(backup, data, 0o600); err != nil {
			return fmt.Errorf("could not write backup %s: %w", backup, err)
		}
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return err
	}
	edit(root)
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// shellQuote quotes a path for a hook "command" string (run by a shell).
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"$`\\!*?[]{}()<>|&;#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
