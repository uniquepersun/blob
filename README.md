# blob

A cozy blob shooter that lives next to your coding agent. Play while Claude
Code (or Codex, Gemini CLI, agy) works; the moment the agent needs you, the
game freezes, focus jumps to the agent's prompt, and when you answer you're
dropped straight back into the match.

```
┌─ claude ──────────────────────┬─ blob ────────────────────────────┐
│ ✻ Working on: fix session bug │ ●●● blob ♥♥♥♥♡ burst 31  ⠹ claude │
│   ⏺ Read(auth.go)             │                                   │
│   ⏺ Read(session.go)          │        (blobs fighting here)      │
│                               │                                   │
│ Do you want to proceed?       │   ╭──────────────────────────╮    │
│ ❯ 1. Yes                      │   │   ◆ claude needs you     │    │
│   2. No                       │   │   wants to use Bash      │    │
└───────────────────────────────┴───────────────────────────────────┘
```

## Install

**Homebrew** (macOS and Linux):

```sh
brew tap uniquepersun/blob https://github.com/uniquepersun/blob
brew trust uniquepersun/blob       # Homebrew 7+ asks before using third-party taps
brew install uniquepersun/blob/blob
```

**Prebuilt binaries**: grab the archive for your OS from
[Releases](https://github.com/uniquepersun/blob/releases), unpack, and put
`blob` on your PATH.

**From source** (Go 1.25+):

```sh
go install github.com/uniquepersun/blob/cmd/blob@latest
```

Then add the agent hooks (backs up your config first):

```sh
blob install claude            # or codex, gemini, agy
```

Hooks store the path of the `blob` binary that installed them, so re-run
`blob install <agent>` if you switch between the brew and `go install`
copies.

## Run it

```sh
blob claude            # Claude Code full screen, the game one keypress away
blob claude --resume   # anything after the agent name goes to the agent
blob codex | blob gemini | blob agy
blob run -- aider      # any other CLI (prompt detection only, no hooks)
blob                   # just the game; put it in its own pane/tab and any
                       # agent with blob hooks installed will pause it
```

The agent gets the whole terminal. A small `ctrl+] blob` chip sits in the
top-right corner and pulses for a few seconds when the agent starts working.
`ctrl+]` (or clicking the chip) opens the game in a panel along the bottom;
`ctrl+]` again hides it. When the agent asks for permission the game hides so
the prompt gets the whole screen, and comes back once you answer. When the
agent finishes: confetti, then the game tucks itself away.

- `BLOB_GAME_PCT=70 blob claude`: game panel height (default 60%)
- `BLOB_LAYOUT=side blob claude`: open the game on the right instead
  (needs 110+ columns)

## Controls

| | |
|---|---|
| `w a s d` | move |
| mouse | aim (hold left button to fire) |
| arrow keys | aim and fire in that direction |
| `space` | fire |
| `e` / `b` / right click | throw a bomb |
| `esc` | the menu: blob look, sound, volume, shot sound, bots, bot skill, your speed, pickups, screen shake, hit words, pixels, game size and layout, controls, new match |
| `p` | pause |
| `m` | sound on / off (remembered) |
| `c` | pick the blob look: classic or simple (remembered) |
| `ctrl+]` | show / hide the game (wrapper); with the panel open and the agent focused, back to the game |
| click a pane | focus it |
| `enter` | dismiss a "needs you / done" card and keep playing |
| `q` | quit (standalone) |

Pickups: **burst** blaster, **scatter** shot, **bombs**, **hearts**, and very
rarely a golden **rubber duck**.

### Sound

Soft, quiet, generated sound effects (no audio files): pops, blips, bombs,
and a chime when the agent needs you or finishes while you're playing (with
`blob claude` and the game hidden, blob stays quiet). `m` mutes and blob remembers; `BLOB_SOUND=0` or `=1` forces
it for one run. `blob sounds` plays every effect once; `blob sounds shots`
compares the shot sounds, and `BLOB_SHOT=pew|plip|puff|tick|off` picks one.

Until you choose, things are a little random: the blob look (classic,
shaded, or simple, flat with outlines) each run, the shot sound (tick or
puff) each run, and each match gets 3 to 8 bots with a fresh cast of names
and colors. The first time the game opens, a banner says where the
settings are. `c` or the esc menu picks a look; the menu has "random" for
each of these if you want the surprise back.
Everything you set in the esc menu is remembered; the `BLOB_*` environment
variables override it for a single run. Audio goes through
[oto](https://github.com/ebitengine/oto) (CoreAudio on macOS, PulseAudio or
ALSA on Linux); with no audio device blob is simply silent.

### Smooth controls

Kitty, Ghostty, WezTerm, foot, recent Alacritty and iTerm2 report key
releases, so holding `w` behaves like a real game. In other terminals blob
guesses "held" from the key-repeat stream, which works but lags a little on
release; the mouse is fully precise everywhere. Inside tmux, key releases are
lost, so run `blob claude` directly in the terminal rather than in a tmux pane.
Use a truecolor terminal (`COLORTERM=truecolor`).

macOS Terminal draws the `▀` half-block character with a gap under it,
which makes the half-block pixel art look striped. blob notices
(`TERM_PROGRAM=Apple_Terminal`) and switches to blocky pixels: one solid
color per cell, half the vertical detail, no stripes. Change it under
**pixels** in the esc menu (auto / fine / blocky).

## Agents

| Agent | Pause on "needs you" | Notes |
|---|---|---|
| Claude Code | `PermissionRequest` and `Notification` hooks | Most complete. `blob install claude` |
| Gemini CLI | `Notification` (ToolPermission) | `blob install gemini` |
| Codex | `PermissionRequest` in `~/.codex/hooks.json` | `blob install codex`, then trust the hooks once via `/hooks` in Codex. **Verify the hooks.json shape against your Codex version.** |
| agy | screen detection in the wrapper | agy only has `PreInvocation/PostInvocation/PreToolUse/PostToolUse/Stop`. Use `blob agy`. |

`blob install <agent> --project` writes the project-level config instead of
the global one. `blob uninstall <agent>` removes only blob's entries.

**agy `--agy-permissions`:** also registers a `PreToolUse` hook on
`run_command`, which pauses the game just before agy asks to run a command.
agy fails closed on bad hook replies, so this hook always answers
`{"decision":"ask"}`, the same as agy's default review policy. If you've set
agy to auto-run commands, this makes it ask again; leave the flag off in
that case.

### How the pieces fit

```
agent ──hook──▶ blob hook <agent> ──one JSON line──▶ unix socket ──▶ game
                (translates payload,                  BLOB_SOCKET or
                 exits in ~10ms, never fails)         $XDG_RUNTIME_DIR/blob-<uid>.sock
```

The wrapper (`blob claude`) runs the agent in a pseudo-terminal, gives it its
own socket through `BLOB_SOCKET`, and draws its screen with
`charmbracelet/x/vt`. It also watches the bottom of the agent's screen for
approval prompts ("Do you want to proceed?", "(y/n)", …) as a fallback for
agents without a permission hook. Tune that with `BLOB_PROMPT_REGEX`.

## Releasing

Push a version tag and GitHub Actions does the rest (GoReleaser builds
macOS and Linux binaries, publishes the release, and updates
`Casks/blob.rb` on main):

```sh
git tag v0.2.0 && git push origin v0.2.0
```

Dry run locally: `goreleaser release --snapshot --clean`.

## The 30-second demo

1. `blob claude`, type a real task. The match starts as soon as Claude works.
2. Play. Grab a burst blaster, throw a bomb.
3. Claude asks to run a command: the game freezes, the card pulses, focus
   jumps to Claude's prompt.
4. Approve. You're back in the match mid-fight.
5. Claude finishes: confetti, then a "done" card.

Rehearse without an agent: run `blob` in one pane and
`blob send attention "wants to use Bash"`, `blob send working`,
`blob send done` in another.

## Development

```sh
go test ./internal/...
go run ./cmd/blobshot -cols 120 -rows 34 wait:2 shot:frame.png -- blob
```

`blobshot` runs any command in a virtual terminal, plays a script of keys,
mouse moves and agent events at it, and saves PNG screenshots (see the top of
`cmd/blobshot/main.go`). Good for README images and regression checks.

```
cmd/blob         the binary: game, wrapper, hook, install
cmd/blobshot     headless screenshot tool
internal/game    simulation: blobs, weapons, bots, springs (no terminal code)
internal/render  half-block pixel renderer, HUD, cards
internal/input   key/mouse → controls, key-release and fallback modes
internal/app     Bubble Tea model, PTY wrapper, prompt detection
internal/hook    per-agent payload translation
internal/install config merging for each agent
internal/ipc     the socket protocol
```

`internal/game` has no terminal imports on purpose, so the same simulation
can run on an SSH server (charmbracelet/wish) when online PvP comes later.
