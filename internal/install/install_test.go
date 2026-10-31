package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readJSON(t *testing.T, p string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func countOurs(m map[string]any, ev string) (ours, theirs int) {
	hooks, _ := m["hooks"].(map[string]any)
	groups, _ := hooks[ev].([]any)
	for _, g := range groups {
		for _, h := range g.(map[string]any)["hooks"].([]any) {
			if isOurs(h.(map[string]any)["command"].(string)) {
				ours++
			} else {
				theirs++
			}
		}
	}
	return
}

func TestClaudeInstallIsIdempotentAndKeepsUserHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".claude", "settings.json")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	existing := `{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"say done"}]}]}}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := Options{Agent: "claude", Bin: "/usr/local/bin/blob"}
	for i := 0; i < 2; i++ {
		if _, err := Run(opts); err != nil {
			t.Fatal(err)
		}
	}
	m := readJSON(t, path)
	if m["model"] != "opus" {
		t.Fatal("unrelated settings must survive")
	}
	if ours, theirs := countOurs(m, "Stop"); ours != 1 || theirs != 1 {
		t.Fatalf("Stop hooks: ours=%d theirs=%d, want 1 and 1", ours, theirs)
	}
	if ours, _ := countOurs(m, "PermissionRequest"); ours != 1 {
		t.Fatalf("PermissionRequest hook missing")
	}

	opts.Uninstall = true
	if _, err := Run(opts); err != nil {
		t.Fatal(err)
	}
	m = readJSON(t, path)
	if ours, theirs := countOurs(m, "Stop"); ours != 0 || theirs != 1 {
		t.Fatalf("after uninstall: ours=%d theirs=%d", ours, theirs)
	}
	if _, ok := m["hooks"].(map[string]any)["PermissionRequest"]; ok {
		t.Fatal("empty event lists should be removed")
	}
	backups, _ := filepath.Glob(path + ".blob-backup-*")
	if len(backups) == 0 {
		t.Fatal("expected a backup file")
	}
}

func TestAgyShape(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := Run(Options{Agent: "agy", Bin: "/opt/my tools/blob"}); err != nil {
		t.Fatal(err)
	}
	m := readJSON(t, filepath.Join(home, ".gemini", "config", "hooks.json"))
	entry := m["blob"].(map[string]any)
	stop := entry["Stop"].([]any)[0].(map[string]any)
	if _, grouped := stop["hooks"]; grouped {
		t.Fatal("agy Stop must be a flat handler list")
	}
	if !strings.HasPrefix(stop["command"].(string), "'/opt/my tools/blob' hook agy Stop") {
		t.Fatalf("command not quoted right: %v", stop["command"])
	}
	post := entry["PostToolUse"].([]any)[0].(map[string]any)
	if post["matcher"] != "*" {
		t.Fatal("agy PostToolUse must be grouped with a matcher")
	}
	if _, ok := entry["PreToolUse"]; ok {
		t.Fatal("PreToolUse is opt-in only")
	}
}

func TestUninstallWithoutFileCreatesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := Run(Options{Agent: "claude", Bin: "blob", Uninstall: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); err == nil {
		t.Fatal("uninstall should not create a settings file")
	}
}
