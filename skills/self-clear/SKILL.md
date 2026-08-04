---
name: self-clear
description: Clear your own Claude Code context with /clear and carry on in the fresh session. Use when the current context has served its purpose — a finished phase, a long exploration, an abandoned approach — and you want a clean slate rather than a summary. Requires a handover written down first, because /clear keeps nothing.
---

# self-clear

Inject `/clear` into the terminal hosting this Claude Code session, then have a
detached sidekick type a resume prompt into the fresh session a few seconds
later — so the run continues on its own with an empty context.

Normally `/clear` can only be typed by the user. This skill is for autonomous
runs where nobody is at the keyboard.

**`/clear` is not `/compact`.** Compaction leaves you a summary; `/clear` leaves
you nothing at all. Everything in the current context — files you read, output
you saw, decisions you made, what you were part-way through — is gone, and the
fresh session starts with a new session ID. Reach for `/compact` when you still
need the thread of the work. Reach for `/clear` when a clean slate is genuinely
better: the current phase is finished, or the context is full of exploration
that turned out to be irrelevant.

## Step 1 — write the handover

Do this **before** invoking. The fresh session knows only what it can read.

Write what the next session needs into a file: what has been done, what is left,
which files matter, decisions already made and why, and the exact next action.
Either

- a handover file such as `next-session.md`, which the resume prompt points at, or
- somewhere the fresh session loads on its own — project memory, `CLAUDE.md`, or
  `AGENTS.md` — which is what makes the bare `continue` form work.

Write it as instructions to someone who was not here. "Continue the refactor" is
not a handover; "`src/auth/token.go` is half-migrated to the new signer — finish
`Verify()`, then run `go test ./auth/...`" is.

## Step 2 — account for anything still running

**Do not skip this.** Background shells, subagents, workflows, and scheduled
tasks outlive the context that started them, but their handles do not: after
`/clear` you no longer know they exist. Take stock of every in-flight child
process and pick one of three for each:

1. **Wait** — it is nearly done, so let it finish and collect the result before
   clearing. Simplest and safest.
2. **Kill** — it is no longer needed. Stop it (`KillShell`, `TaskStop`, or
   whatever your harness provides) before clearing, so it does not run on
   unwatched.
3. **Hand it over** — it must keep running. Then the handover file must carry
   its shell/task ID, what it is doing, when it is expected to finish, and how
   to collect its result; and the resume prompt must tell the fresh session to
   go on monitoring it.

Option 3 is the fragile one. A new session may not be able to attach to a shell
it never started, so do not rely on the ID alone: prefer background work that
writes its output to a file (`... > result.txt 2>&1`), and record that path in
the handover. If a job is already running without such a redirect, prefer
waiting for it or killing it over handing it over.

## Step 3 — invoke

This skill is a native binary, `self-clear`, installed on `PATH`. Two forms.

With a resume prompt, read from stdin:

```bash
self-clear - <<'EOF'
read next-session.md and continue from the "Next steps" section
EOF
```

Without one — the fresh session is prompted with `continue`:

```bash
self-clear
```

If this skill was invoked with an argument, that argument is the resume prompt;
pass it through the stdin form. Use the bare form only when the handover lives
somewhere the fresh session picks up by itself (project memory, `CLAUDE.md`,
`AGENTS.md`) — on its own, `continue` says nothing about what to continue.

The prompt is read from **stdin** (the `-` form), not an argument — this avoids
shell quoting, escaping, and length limits. Prefer the Bash-tool heredoc shown
above; piping a prompt that contains CJK or special characters through
PowerShell 5.1 can mangle the encoding. A bare `self-clear` never reads stdin,
so it won't block waiting for input. Keep the prompt to one line and short —
point at the handover file rather than restating it.

The binary prints three lines once `/clear` is queued in Claude Code's input:

```
target claude pid=<N>
injected /clear via console (pid <N>).
sidekick spawned (windowless, background); it will prompt the fresh session in 8s with: <prompt>
```

## Step 4 — end your turn immediately

`/clear` is processed by Claude Code once your current turn ends, and the
sidekick types the resume prompt 8 seconds later. So after the binary returns:
stop. No further tool calls, no closing summary — anything you say now is wiped
along with everything else, and work done past that point can collide with the
clear. End the turn, and pick the thread back up in the fresh session.

## Troubleshooting

**`command not found` / `self-clear is not recognized`** — the binary isn't
installed, or isn't on `PATH`.

**Nothing arrives after the clear** — the sidekick's log is at
`%TEMP%\self-clear-sidekick.log`; it records the injected prompt or the failure.
The session is idle, not broken: the user can simply type the prompt.

**A stuck sidekick** can be listed and killed with:

```powershell
Get-CimInstance Win32_Process -Filter "Name='self-clear.exe'" |
  Where-Object { $_.CommandLine -like '*sidekick*' } |
  ForEach-Object { Stop-Process -Id $_.ProcessId -Force }
```
