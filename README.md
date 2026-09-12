# self-clear

Lets a Claude Code session run `/clear` on itself — and then keep working in the
fresh session. Useful for long autonomous runs where nobody is at the keyboard
to type the slash command, and where a summary is not what you want: you want
the context actually gone.

## Why this exists

`/clear` wipes the conversation and starts a new session. Today it can only be
typed by the user. A model that has finished a phase, or has filled its context
with exploration that turned out to be irrelevant, cannot reset itself — it can
only compact (which keeps a summary of what it wanted to drop) or ask the user
to intervene.

self-clear is the missing option. The model injects `/clear` into the terminal
hosting its own session via focus-free Win32 console input. Slash commands are
processed by Claude Code, not the model, so the injected command takes effect
once the model's turn ends. Because `/clear` leaves the new session sitting idle
with no context at all, a detached, windowless sidekick types a resume prompt a
few seconds later — so the run continues by itself against a clean slate.

The model is expected to write a handover (a file, `CLAUDE.md`, or project
memory) before clearing; the resume prompt points the fresh session at it. The
bundled skill walks the model through that, including what to do about
background shells and subagents that outlive the context which started them.

## Requirements

- **Windows.** Injection uses Win32 console APIs (`AttachConsole` /
  `WriteConsoleInputW`). macOS/Linux are not supported.
- **Claude Code**, running in a console (Windows Terminal or legacy conhost).
  Works even when Claude Code runs elevated. No other agent will do: the program
  locates its host by walking up the process tree to a `claude.exe` ancestor, and
  the whole design rests on Claude Code queueing an injected slash command until
  the current turn ends.
- **[Go](https://go.dev/dl/) 1.21+** on your `PATH`. The skill runs from source
  with `go run`; there is no separate binary to install or keep up to date.
- **[Node.js](https://nodejs.org/)** to run `npx skills` for the install.

## Install

```
npx skills add neritina-ai/self-clear --skill self-clear -a claude-code -g -y
```

The [skills CLI](https://github.com/vercel-labs/skills) can install a skill into
any agent it knows about; the flags above pin it to a global Claude Code
install, which is the only one this skill can work in. Repeat the `-g` on the
update and uninstall commands below: without it they act on the current project
instead, and `remove` reports that there is nothing to remove.

That drops the skill in `~/.claude/skills/self-clear/`. The next session picks
it up, and the model uses it when the description matches — or you can invoke it
directly with `/self-clear` (optionally followed by the resume prompt for the
fresh session).

Nothing is compiled at install time. The first invocation runs `go run .` on
`scripts/self-clear.go`, which takes a few seconds; Go caches the build, so
later invocations take about a third of a second.

### To update

```
npx skills update self-clear -g
```

Go rebuilds by itself whenever the source changes, so there is no second step.

### To uninstall

```
npx skills remove self-clear -g
```

Everything self-clear writes lives in one directory, which nothing else uses and
which you can delete at any time:

```powershell
Remove-Item -Recurse -Force $env:LOCALAPPDATA\self-clear
```

### Coming from the two-part install

self-clear used to ship as a `go install`ed binary plus a separately installed
skill. Both are replaced by the single `npx skills add` above, so remove the old
pieces: `npx skills remove self-clear -g`, then `rm ~/go/bin/self-clear.exe`
(and `rm $env:TEMP\self-clear-sidekick.log`, the log's old home).

## Usage

The skill is written for the model, and `skills/self-clear/SKILL.md` is the full
version. In short — the model:

1. Writes a handover somewhere the next session can read it (`next-session.md`,
   project memory, `CLAUDE.md`, `AGENTS.md`).
2. Decides what to do with anything still running in the background — wait for
   it, kill it, or record in the handover how the fresh session should keep
   monitoring it.
3. Invokes the skill, with or without a resume prompt:

   ```bash
   go -C $HOME/.claude/skills/self-clear/scripts run . - <<'EOF'
   read next-session.md and continue from the "Next steps" section
   EOF
   ```

   ```bash
   go -C $HOME/.claude/skills/self-clear/scripts run .
   ```

   The bare form resumes the fresh session with `continue`, which only works if
   the handover is somewhere that session loads on its own.
4. Ends its turn immediately.

## How it works

1. Walk the current process's parent chain to the hosting `claude.exe` (the
   program runs as a descendant: `claude.exe → shell → go run → self-clear`).
2. `FreeConsole()` → `AttachConsole(claude_pid)` → open `CONIN$` →
   `WriteConsoleInputW()` the keystrokes `/clear` + Enter straight into that
   console's input buffer. No window focus, foreground switch, IME, or clipboard
   is involved — which is why it's invisible and works even against an elevated
   console.
3. The slash command lands in Claude Code's input and takes effect once the
   current model turn ends — which is why the skill tells the model to **end its
   turn immediately after invoking**.
4. The program copies its own executable to
   `%LOCALAPPDATA%\self-clear\self-clear-sidekick.exe` and starts that copy
   detached and windowless (`DETACHED_PROCESS | CREATE_NO_WINDOW`), handing it
   the resolved pid and the resume prompt. The sidekick waits 8 seconds — long
   enough for the turn to end and the clear to happen — then injects the prompt,
   which arrives as the fresh session's first user message. It logs to
   `sidekick.log` in that same directory.

Locating the host by process ancestry (rather than enumerating windows) is what
makes the injection focus-free and elevation-tolerant. The resume prompt is
handed to the sidekick rather than re-derived, because once the main process
exits, the ancestry chain back to `claude.exe` is broken.

The sidekick runs from a copy because the program cannot count on its own
executable outliving it. Whenever the source has changed, `go run` links into a
fresh temporary directory and deletes it the moment the program exits: a sidekick
started from there would hold that file open, `go run`'s cleanup would fail, and
the command would exit non-zero even though the injection had worked. When the
source is unchanged Go runs the linked executable straight out of its build
cache, which does survive — but the program has no way to tell the two cases
apart, and the build cache is not somewhere anything should be spawned from in
any event. So it keeps its own copy. That copy is a single file, rewritten on
each run; copying ~2 MB costs about 3 ms.

## Caveats

- **Windows only.** No macOS/Linux support.
- **The context is really gone.** There is no summary and no undo. If the
  handover was not written first, the run resumes with nothing to go on.
- **Sessions started with `claude --bg` cannot be reached.** They live behind a
  private ConPTY; `AttachConsole` still succeeds, so the injection reports
  success while nothing actually happens. Only a Claude Code with its own console
  can be injected into.
- **Pending text in the input box at injection time:** the typed `/clear` is
  appended after whatever is already there, so the input no longer starts with
  `/` and Claude Code treats it as a normal message instead of a slash command.
  Keep the input box empty (the typical autonomous case).
- **The turn must end promptly after invoking.** The sidekick's fixed 8-second
  wait assumes the model stops right after injecting; working past it can cause
  the resume prompt to be typed into the session that is about to be cleared,
  and lost with it.
- **Background children do not hand themselves over.** Shells and subagents
  spawned by the old session keep running, but the fresh session has no handles
  to them. Have long jobs write their output to a file, and name that file in
  the handover.

## Development

The skill is a single Go file, `skills/self-clear/scripts/self-clear.go`, with no
dependencies beyond the Windows standard library. Install a working copy straight
from a clone:

```
npx skills add . --skill self-clear -a claude-code -g -y
```

To check that it can find the host console without actually clearing anything:

```
go -C skills/self-clear/scripts run . --dry-run
```

Written by **Claude Opus 5.0** (model ID `claude-opus-5`), Anthropic's Claude
Opus 5, driving Claude Code.

## License

MIT — see `LICENSE`.
