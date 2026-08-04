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
- **Claude Code running in a console** (Windows Terminal or legacy conhost).
  Works even when Claude Code runs elevated.
- **[Go](https://go.dev/dl/) 1.26+** to install the binary, with `~/go/bin` on
  your `PATH`.
- **[Node.js](https://nodejs.org/)** to run `npx skills` for the skill half.

## Install

Two components, installed separately: the binary and the skill.

**1. Install the `self-clear` binary** (puts `self-clear.exe` in `~/go/bin`):

```
go install github.com/neritina-ai/self-clear/cmd/self-clear@latest
```

Make sure `~/go/bin` (the default `GOBIN`) is on your `PATH` so the skill can
find `self-clear`. To verify it runs, type `self-clear` in a terminal **outside**
a Claude Code session — it should print `no claude.exe ancestor found`, which
confirms the binary works. (Inside a session it would do the real thing.)

**2. Install the `self-clear` skill:**

```
npx skills add neritina-ai/self-clear
```

The [skills CLI](https://github.com/vercel-labs/skills) will ask which agents to
install into and whether to install globally; pick Claude Code. To skip the
prompts:

```
npx skills add neritina-ai/self-clear --skill self-clear -a claude-code -g -y
```

That drops the skill in `~/.claude/skills/self-clear/`. The next session picks
it up, and the model uses it when the description matches — or you can invoke it
directly with `/self-clear` (optionally followed by the resume prompt for the
fresh session).

### To update

```
go install github.com/neritina-ai/self-clear/cmd/self-clear@latest
npx skills update self-clear
```

### To uninstall

```
npx skills remove self-clear
rm ~/go/bin/self-clear.exe
```

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
   self-clear - <<'EOF'
   read next-session.md and continue from the "Next steps" section
   EOF
   ```

   ```bash
   self-clear
   ```

   The bare form resumes the fresh session with `continue`, which only works if
   the handover is somewhere that session loads on its own.
4. Ends its turn immediately.

## How it works

1. Walk the current process's parent chain to the hosting `claude.exe` (the
   binary runs as a descendant: `claude.exe → shell → self-clear`).
2. `FreeConsole()` → `AttachConsole(claude_pid)` → open `CONIN$` →
   `WriteConsoleInputW()` the keystrokes `/clear` + Enter straight into that
   console's input buffer. No window focus, foreground switch, IME, or clipboard
   is involved — which is why it's invisible and works even against an elevated
   console.
3. The slash command lands in Claude Code's input and takes effect once the
   current model turn ends — which is why the skill tells the model to **end its
   turn immediately after invoking**.
4. The binary spawns itself as a detached, windowless sidekick
   (`DETACHED_PROCESS | CREATE_NO_WINDOW`), handing it the resolved pid and the
   resume prompt. The sidekick waits 8 seconds — long enough for the turn to end
   and the clear to happen — then injects the prompt, which arrives as the fresh
   session's first user message. It logs to `%TEMP%\self-clear-sidekick.log`.

Locating the host by process ancestry (rather than enumerating windows) is what
makes the injection focus-free and elevation-tolerant. The resume prompt is
handed to the sidekick rather than re-derived, because once the main process
exits, the ancestry chain back to `claude.exe` is broken.

## Caveats

- **Windows only.** No macOS/Linux support.
- **The context is really gone.** There is no summary and no undo. If the
  handover was not written first, the run resumes with nothing to go on.
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

Build and install locally from a clone:

```
go install ./cmd/self-clear
npx skills add . --skill self-clear -a claude-code -g -y
```

Written by **Claude Opus 5.0** (model ID `claude-opus-5`), Anthropic's Claude
Opus 5, driving Claude Code.

## License

MIT — see `LICENSE`.
