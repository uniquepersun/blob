# CLAUDE.md: blob

Context handoff from the claude.ai chat where this project was designed and
first built. Read this before changing anything.

## What this is

`blob` is a cozy top-down blob shooter that runs in the terminal next to a
coding agent (Claude Code, Codex, Gemini CLI, agy). You play while the agent
works. When the agent needs you (permission prompt) the game freezes and focus
jumps to the agent's prompt; when you answer, you're dropped back into the
match. When the agent finishes, confetti, then a "done" card.

- Built for the **DevF8rge × Hacktoberfest 2026 hackathon, Bengaluru**.
  Themes were AI for Open Source / Open Source for Education / Developer
  Productivity. We pitch it under **Developer Productivity**: it removes the
  dead time and context switching of waiting on agents (people otherwise grab
  their phone). It is open source with a pluggable adapter per agent, which
  touches the open-source angle too.
- Owner: Abhay (college student; strong in Linux systems programming, Rust,
  Python, JS; has built a multiplayer SSH terminal game in Go before).
- Status: v0.1 works on Abhay's machine. Currently developing on macOS
  (Apple Silicon, zsh, Ghostty, Go 1.27); the original build was on Linux
  with fish.

## Working preferences (Abhay)

- Give complete file replacements, not diff-style patches.
- Commits are Abhay's alone: never add `Co-Authored-By: Claude` or any
  Claude attribution to commits or PRs.
- Always fetch/pull before pushing (Abhay edits the repo on GitHub too), and
  don't push without being asked.
- Be direct and technically substantive; push back clearly when something
  won't work as asked, and offer the alternative.

## The pitch / demo (this is the whole point, protect it)

1. `blob claude`, type a real task; Claude has the full screen and the corner
   chip pulses "claude's working · ctrl+] play blob".
2. `ctrl+]`: the game opens along the bottom. Grab a burst blaster, throw a bomb.
3. Claude asks to run a command: the game hides, Claude gets the whole screen
   and focus.
4. Approve: the game comes back and you're in the match mid-fight.
5. Claude finishes: confetti, then the game tucks away (chip shows ✓).

Rehearse without an agent: `blob` in one pane, then
`blob send attention "wants to use Bash"`, `blob send working`,
`blob send done` in another.

## Scope decisions

- **Local only for now.** Bots by default, no online PvP yet. Online comes
  later (plan: SSH server with charmbracelet/wish; `internal/game` has zero
  terminal imports specifically so it can run server-side).
- **CLIs first, easiest first:** Claude Code → Gemini CLI → Codex → agy.
- **Antigravity means the `agy` CLI**, not the Antigravity IDE.
- "Comforting" beats "competitive": no death, blobs pop into confetti and
  respawn; bots are deliberately gentle (numbers below).

## Key decisions and why

### Go + Charm instead of Rust
Abhay is strong in Rust, but Charm ships every needed piece from one vendor:
Bubble Tea v2 (key release events, cell-diffing "Cursed Renderer"), Harmonica
(spring physics for squash/stretch), x/vt (terminal emulator for the wrapper),
Wish (SSH, for later online play). There are reference projects doing PTY +
x/vt inside Bubble Tea (termview, TERGO). Abhay also has Go experience from his
SSH game. A 2D blob arena doesn't need Rust's performance (a frame renders in
~0.35ms).

### You can't draw inside an agent's TUI → PTY wrapper
Claude Code / Codex / agy own their terminal screen; a plugin can't render a
game inside it. Options considered: tmux/zellij split, tmux display-popup,
kitty/wezterm splits, separate window. Chosen: **`blob <agent>` runs the agent
in a pseudo-terminal (creack/pty), parses its output with charmbracelet/x/vt,
and draws agent + game in one terminal** (game hidden behind a corner chip
until you open it; see the state machine below). Why:
- works with any CLI, hooks or not (`blob run -- <cmd>`);
- blob owns the real terminal, so the Kitty keyboard protocol (key releases)
  works; tmux drops key-release events;
- it can read the agent's screen to detect approval prompts (fallback for
  agents without a permission hook, i.e. agy);
- per-session socket via `BLOB_SOCKET` env, inherited by the agent's hooks, so
  hooks reach the right game window.
Standalone mode (`blob`) still exists for people who prefer their own pane.

### Hooks: one binary, tiny and never-failing
Every agent calls `blob hook <agent> [event]`. It reads the payload (stdin, or
argv JSON for Codex legacy notify), translates it in `internal/hook`, writes one
JSON line to a Unix socket, exits. ~10ms, always exit 0, silent if no game is
listening. Reason: agent hooks run synchronously and block the agent (agy
especially); a hook must never slow or break the agent.

### Terminal input problem (no key releases in most terminals)
- **Precise mode:** Bubble Tea v2 `KeyboardEnhancements.ReportEventTypes`
  gives `KeyReleaseMsg` on terminals with the Kitty protocol (kitty, Ghostty,
  WezTerm, foot, recent Alacritty/iTerm2). Not Terminal.app, not inside tmux.
- **Fallback mode:** a key counts as held until its auto-repeats stop
  (0.55s after first press to bridge the OS repeat delay, 0.14s after repeats).
- **Mouse is precise everywhere** (SGR mouse reports button release): hold
  left to fire, cursor aims. Arrow keys = aim+fire (twin-stick on keyboard).
- Gamepad (gilrs-equivalent) and OS-level key reading were considered;
  OS-level was rejected as creepy (needs input-monitoring permission and sees
  keys typed to the agent).

### Look and feel
- Half-block rendering (`▀` with fg = top pixel, bg = bottom pixel): square-ish
  pixels, 2× vertical resolution. Truecolor required.
- Palette: blob colors taken from the DevF8rge slides (periwinkle #8AB4E8,
  mustard #F5B731, coral #EC7D7A, tomato #E8432F) on a dark misty dusk
  background (Silksong-ish mood: vignette, faint lichen speckles, drifting
  warm spores).
- Squash and stretch: on fire, `Scale` drops instantly to ~0.74 and an
  underdamped Harmonica spring (freq 11, damping 0.28) overshoots past 1 then
  settles = the "jerky shrink and grow" Abhay asked for. `Stretch` elongates
  along velocity and squishes on hits/walls (freq 8, damping 0.42).
  A test asserts the overshoot.
- Respawn "pop-in" from scale 0.05, happy blush after pickups, eyes follow aim,
  blink, idle breathing, hit flash, screen shake on bombs.

### Gentleness tuning (measured, not guessed)
First version: an idle player popped in ~8s. Fixes: 2s spawn shield, bot
cooldown ×1.7, bot burst on 0.4–1.0s / off 1.0–2.4s, bots treat the player as
2.2× farther when picking targets, avoid piling onto an already-chased blob,
keep +10px extra distance from the player, aim wobble ±0.42 rad vs player
(±0.28 vs bots). Result: idle player mean time-to-pop **25.3s** (40 seeds);
`BLOB_TUNE=1 go test ./internal/game -run Tune -v` re-measures. A test
checks all three bots chase the player <15% of the time.

## Agent research findings (as of Oct 2026)

- **Claude Code:** richest hooks. We use UserPromptSubmit, PreToolUse,
  PostToolUse → working; PermissionRequest → attention; Notification →
  attention (`notification_type: idle_prompt` → done); Stop → done;
  SessionEnd → ended. Config: `~/.claude/settings.json` (or project
  `.claude/settings.json`). Hooks load at startup: restart Claude or open
  `/hooks` after installing. We never print a decision for PermissionRequest,
  so Claude's normal dialog still shows.
- **Codex:** has `hooks.json` (~/.codex/hooks.json or .codex/hooks.json) since
  early 2026, Claude-like shape and event names incl. PermissionRequest/Stop;
  hooks must be trusted once via `/hooks` in Codex. Legacy `notify = [...]` in
  config.toml passes JSON as an argv argument (`type: agent-turn-complete`);
  handled too. **The exact hooks.json shape is unverified; check against the
  installed version.**
- **Gemini CLI:** hooks in `~/.gemini/settings.json`; Notification with
  `notification_type: ToolPermission`; AfterAgent = done. Timeouts in ms.
  (Google moved consumer Gemini CLI users to agy in June 2026.)
- **agy (Antigravity CLI):** only 5 events: PreInvocation, PostInvocation,
  PreToolUse, PostToolUse, Stop. **No permission/notification event.** Hooks
  in `~/.gemini/config/hooks.json` (global) or `.agents/hooks.json`
  (workspace), keyed by hook name (we use `"blob"`). Quirks that bit others:
  payload has no event name (pass it as argv); PreToolUse/PostToolUse are
  grouped `{matcher, hooks:[...]}` but PreInvocation/PostInvocation/Stop are
  flat handler lists (wrong shape → agy rejects the whole file); hooks must
  print JSON on stdout; agy **fails closed** on a bad PreToolUse reply.
  Default install registers only passive events. `--agy-permissions` adds
  PreToolUse on `run_command` replying `{"decision":"ask"}` (never "allow").
  Main attention signal for agy = wrapper screen detection.

## Architecture

```
cmd/blob         single binary: play, <agent> wrapper, run --, hook, install,
                 uninstall, send, version
cmd/blobshot     headless tester: runs a command in a virtual terminal, plays
                 scripted keys/mouse/agent events, saves PNG/text screenshots
internal/game    pure simulation (no terminal code): World, Blob, weapons,
                 pickups, bots, particles, springs. Fixed step 1/60s.
internal/render  half-block canvas, overlay text, HUD, pause card, palette
internal/input   keys/mouse → game.Controls; precise vs fallback modes
internal/app     Bubble Tea v2 model, layout, focus, pause state machine,
                 PTY wrapper (wrap.go), prompt-detection regex
internal/hook    per-agent payload → ipc.Event (the only agent-specific code)
internal/install merges blob's hooks into each agent's config (backup first,
                 idempotent, only touches commands containing "blob" + " hook ")
internal/ipc     Unix socket, one JSON line per event; kinds: working,
                 attention, done, ended
```

### App state machine (internal/app/model.go)
- Standalone: pause reasons none, manual (p/esc), attention, done.
  attention → pause; done → celebrate, pause 1.3s later; working → unpause.
- Wrapper (Abhay's design, Oct 31): the game is a panel that is **hidden by
  default**; hidden counts as paused. `m.open` = panel visible.
  - Hidden: agent gets the full terminal; a chip `● ctrl+] blob` sits in the
    agent's top-right corner on row 0 (top-right because Claude Code's live
    area is at the bottom). Clicking it opens the game.
  - working transition (not every tool call) while hidden → chip grows and
    pulses ("claude's working · ctrl+] play blob") for 5s. Never auto-opens.
  - `ctrl+]`: hidden → open + focus game; open+game focused → hide;
    open+agent focused → focus game.
  - Open: game panel along the bottom, `BLOB_GAME_PCT` (default 60%) of rows,
    agent keeps ≥8 rows. `BLOB_LAYOUT=side` restores the old right-hand panel
    (needs ≥110 cols). The agent pty is resized on every open/close, so the
    agent redraws to fit (accepted: you need to see its output).
  - attention while open → hide, set `reopen`; next working → reopen + focus
    game (the "back mid-fight" demo beat). Attention while hidden → nothing.
  - done while open → confetti with agent focused, hide 1.3s later.
- Wrapper prompt scan every 0.3s on the bottom 14 lines; needs 2 consecutive
  hits to raise attention; clears to working only if the scanner itself saw
  the prompt and it then disappears. Override regex with `BLOB_PROMPT_REGEX`.
- ctrl+g avoided for the switch key: Claude Code uses it. Click a pane
  to focus it. In wrapper, ctrl+c in the game shows a toast instead of quitting
  (ctrl+c belongs to the agent).
- The wrapper strips OSC 0/1/2 (window titles) from agent output before the
  emulator (`oscFilter` in wrap.go): x/vt v0.1.0 prints part of any title with
  non-ASCII (Claude Code's "✳ ...") into the screen. blobshot's own emulator
  has the same bug with blob's title, so text shots may show a stray
  "… done · blob" at the agent cursor; that is blobshot-only.
- Socket: standalone `$XDG_RUNTIME_DIR` or tmpdir `/blob-<uid>.sock`;
  wrapper `blob-<uid>-<pid>.sock`, exported to the agent as `BLOB_SOCKET`.

### Esc menu (internal/app/menu.go)
- Abhay: "if I click esc, it should open the menu for the complete game".
  esc opens it (p is the plain pause now). Rows: resume, blob look, sound,
  volume 25-100% (75 = old level), shot sound (plays a preview), bots
  random/1-8 (starts a new match), bot skill gentle/normal/spicy, your
  speed slow/normal/fast (0.8/1/1.25), pickups fewer/normal/more
  (0.6/1/1.6), screen shake, hit words, game size 40-80% and game panel
  below/right (wrapper only), controls, new match, hide game / quit blob.
  ↑/↓ select, ←/→ change, enter, esc. Scrolls (▲/▼) in short panes.
- Gameplay knobs are game.Tuning multipliers (1.0 = the measured
  default); app.tuning() maps menu levels to them and every new match
  reapplies them. Bot skill also scales BotShy (the 2.2× "player is
  farther" factor). Measured idle survival, 5 bots: gentle 28s, normal
  24s, spicy 17s (idle players mostly die to crossfire, so an active
  player feels the gap more). Hit words are Popup.HitWord, hidden in
  render; shake off is render-only too.
- Every change applies live and saves to prefs (style, muted, shot, bots,
  game_pct, side). main.go: prefs first, BLOB_* env overrides one run.
- Rendered by render.drawMenu (UI.Menu), dims the arena like a pause.

### Randomness (Abhay, Oct 31)
- Unchosen = random: look (each run), shot sound (each run, tick or puff:
  sound.RandomShots/PickShot), bot count (each match, 3-8: matchBots).
  Bots also get shuffled names/colors every match (8 of each; lilac,
  peach, mint added). The menu offers "random" for look, shot and bots,
  shown as "random (what it rolled)". prefs "" / 0 mean random.
- No first-run picker any more: the first time the game is on screen a
  one-time welcome banner ("esc opens the menu ...", 7s, non-blocking)
  shows and prefs.Tipped is saved.
- Gentleness with more bots (BLOB_TUNE run): idle player mean time-to-pop
  25.3s with 3 bots, 21.8s with 5, 21.2s with 8. Left untuned.

### Blob look (render.Style)
- Two looks, player's choice (Abhay prefers classic): `classic` = the
  original shaded blob (drawBlobClassic, with HP pips) and `simple` = flat
  body + outline (cream for the player) + tall eyes (drawBlob).
- `c` opens the "pick your blobs" card near the top over the undimmed,
  frozen arena (World.Settle skips the spawn pop-in); ←/→ flips live,
  enter keeps, esc cancels. Space does NOT confirm (it's fire). Saved via
  internal/prefs.
- The floor speckles stayed at the quieter (halved) density for both.

### Sound (internal/sound)
- Abhay's call: on by default, quiet (master 0.45), `m` toggles and is
  saved to `os.UserConfigDir()/blob/settings.json`; `BLOB_SOUND=0/1`
  overrides without saving.
- All effects synthesized at startup in synth.go (sine/tri/soft-square
  tones with glide + exp decay, filtered noise for booms/whooshes). Tune
  there; `blob sounds` plays each one. Tests check peak, NaN, silent tails.
- Shots: Abhay disliked the first (square-wave) shot. Candidates in
  synth.go `shotSound`: pew (asked twice for "more aggressive":
  near-square 1900→190Hz + octave-down grit + noise crack + kick), plip,
  puff, tick, off; also chosen in the esc menu;
  `blob sounds shots` auditions them, BLOB_SHOT picks. Shoot/Hit/Pop get
  5 random pitch variants (±8%) so repeats don't sound stamped.
- One oto v3.5 stream, our mixer is its io.Reader (float32 mono 44.1k,
  ~23ms buffer, tanh soft clip, per-effect min gap so burst fire doesn't
  buzz). oto 3.5 is cgo-free on macOS (purego) and Linux (pure-Go
  PulseAudio, ALSA via purego dlopen), so CGO_ENABLED=0 cross-builds work
  for releases. Verified: cross-compiles for darwin+linux amd64/arm64;
  `blob sounds` opened the device on the Mac. Not verified: Linux audio,
  and whether ALSA prints to stderr on a box with no sound device.
- internal/game stays audio-free: it appends `game.Sound` events to
  `World.Sounds` (player-involved shots/hits/pickups/throws, all pops and
  booms); the app drains them each tick and maps to sound IDs. Agent sounds
  come from the app: Chime on attention, Done on done, but in the wrapper
  only while the game is open (Abhay: no sounds while the game is hidden;
  Claude's folder-trust dialog used to chime at startup). The nudge is
  visual only. Standalone always chimes.
- Abhay hit "sounds played at start, then never again" in a real session;
  not reproducible headlessly. oto's macOS context stops for good after an
  AudioQueue error (one context per process, can't recreate), so
  `watch()` checks every 0.5s (ctx/player errors, stopped player, no
  samples pulled for 1s → Suspend+Resume) and logs to BLOB_SOUND_LOG.
  Root cause still unknown until a log from a real run comes back.
- `blob hook` never touches audio (sound.New only in play()); hook latency
  measured 7.2ms with oto linked (6.2ms before).

### Releases (planned)
- GitHub repo is `uniquepersun/blob` (renamed from Side-game on Oct 31);
  module path `github.com/uniquepersun/blob`. Abhay edits the repo on
  GitHub too (e.g. .github/FUNDING.yml): always fetch/pull before pushing.
- GoReleaser (.goreleaser.yaml) + .github/workflows/release.yml: a `v*`
  tag builds darwin/linux × amd64/arm64 (CGO_ENABLED=0), publishes the
  GitHub Release, and commits the Homebrew cask to Casks/blob.rb on main
  of this same repo (Abhay asked for no separate tap repo; that also means
  no extra token, GITHUB_TOKEN suffices). Install:
  `brew tap uniquepersun/blob https://github.com/uniquepersun/blob` then
  `brew install uniquepersun/blob/blob`. Casks replaced formulas in
  GoReleaser 2.10+; the binary isn't notarized, so the cask's
  postflight_steps clears com.apple.quarantine on macOS.
- Each release pushes a bot commit to main: pull before working.
- version is `var version = "dev"` in main.go, set by ldflags.
- License: MIT (abhay tomar, 2026).

### Gameplay numbers
Arena = pane cols × (rows−2)×2 pixels (top row HUD, bottom row hints).
Blob radius 4px, player speed 36px/s, bots 28. HP 5. Weapons: pea (default,
0.3s), burst (0.085s, 40 ammo), scatter (5 pellets, 0.55s, 10 ammo), bombs
(fuse 1.1s, radius 13, up to 3 dmg). Pickups every 3.5–6.5s, max 4, live 18s;
weights burst 30, scatter 24, bomb 26, heart 15, duck 5 (golden rubber duck:
8s rainbow rapid-fire). Respawn 1.4s.

Easter eggs: konami code (party hats + falling confetti), golden rubber duck,
sleepy "z"s on paused blobs, typing `sudo` → "nice try. blobs don't do sudo".

## Verified vs not verified

Oct 31 (macOS): hidden → nudge → open → attention hides → answer reopens →
done confetti → hides, verified with blobshot and a fake agent script that
sends real events; `internal/app` now has tests for the panel state machine
and the OSC filter.

Earlier, verified headlessly with blobshot (Linux, x/vt terminal): full demo loop with a
fake Claude calling the real `blob hook claude` (waiting → playing → attention
with focus jump → resume with focus back → done); stacked and side layouts;
party mode; hook latency ~10ms with no listener; installer idempotency, backup,
preserving user hooks, agy flat/grouped shapes; 30 tests pass
(`go test ./internal/...`); every frame line is exactly pane width (test).

Not verified:
- Kitty-protocol precise mode (the test terminal doesn't support it, so only
  the fallback ran). Test in Ghostty/Kitty/WezTerm, outside tmux.
- Real Claude Code / Codex / Gemini / agy runs (only a fake agent). Codex
  hooks.json shape especially.
- Prompt regex against real agent screens (tuned on a fake Claude prompt).
- macOS: builds and tests pass; default socket path (~70 chars under
  $TMPDIR) fits the 104 limit. Not yet run interactively.

## Build notes

- First build happened in a sandbox that couldn't reach charm.land /
  golang.org, so go.mod had a `replace` block pointing at local GitHub clones.
  Abhay removed it on his machine. If you see it, delete it and `go mod tidy`.
- Bubble Tea pinned to v2.0.2 because the sandbox had Go 1.24 (v2.1 needs Go
  1.26). Abhay has Go 1.27, so `go get charm.land/bubbletea/v2@latest` is fine;
  the v2 API used here is unchanged.
- x/vt v0.1.0 is labeled experimental by Charm: pin versions, don't upgrade
  mid-hackathon. All emulator calls happen on the Bubble Tea goroutine; PTY
  reader/writer goroutines only move bytes and `Send` messages. The emulator's
  reply pipe blocks until read, so the `io.Copy(ptmx, emu)` goroutine must
  always run.
- Hooks store the absolute path of the binary that ran `blob install`. If the
  binary moves, re-run `blob install <agent>`.
- Screenshot tool: `go run ./cmd/blobshot -cols 120 -rows 34 wait:2
  shot:frame.png -- blob` (steps documented at the top of its main.go). Its
  bitmap font lacks ●♥│ glyphs; they show as "0" in PNGs only.

## Next steps (suggested order)

1. Test with real Claude Code in Ghostty/Kitty: precise keys, permission flow,
   Stop → done card, `idle_prompt` behavior, resize while running.
2. Tune `defaultPromptRe` in internal/app/wrap.go against real Claude and agy
   screens.
3. Verify Codex hooks.json shape; then Gemini; then agy via wrapper.
4. Polish for the pitch: README GIF via blobshot or VHS, maybe sound-free
   "bell" on attention, a zoom key for the focused pane.
5. Later: online PvP over SSH with wish, reusing internal/game server-side.
