// Command self-clear injects Claude Code's /clear slash command into the
// terminal hosting the current session, then spawns a detached, windowless
// "sidekick" that types a resume prompt into the fresh session a few seconds
// later -- so an autonomous workflow keeps going after its context is wiped.
//
// Two public forms:
//
//	self-clear        clear, then resume the fresh session with "continue"
//	self-clear -      read the resume prompt from stdin
//
// One internal form (spawned by the above):
//
//	self-clear sidekick <pid> <prompt>   wait, then inject <prompt> into <pid>
//
// Injection is focus-free: it walks its own process ancestry to the hosting
// claude.exe, AttachConsole()s to it, and WriteConsoleInputW()s straight into
// that console's input buffer -- the reason no window focus is needed and why
// it works even against an elevated console. The sidekick is launched with
// DETACHED_PROCESS|CREATE_NO_WINDOW on this native console-subsystem binary, so
// the OS never gives it a console window.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/neritina-ai/self-clear/internal/coninject"
)

const (
	// Windows CreateProcess flags. DETACHED_PROCESS gives the child no console
	// at all; CREATE_NO_WINDOW suppresses a window for any console it might
	// otherwise get. Together, on a native console-subsystem exe, the sidekick
	// runs truly windowless.
	detachedProcess = 0x00000008
	createNoWindow  = 0x08000000

	// enterDelay is the pause between typing the command text and pressing Enter,
	// long enough for Claude Code's input to register the text first.
	enterDelay = 150 * time.Millisecond

	// sidekickDelay is how long the sidekick waits before injecting the resume
	// prompt. It must outlast the gap between this process returning and /clear
	// actually running, which happens only once the model's turn has ended --
	// text injected before that would be typed into the old session and wiped
	// along with it.
	sidekickDelay = 8 * time.Second

	// defaultPrompt is injected when the caller supplies none. It assumes the
	// handover lives somewhere the fresh session reads on its own (CLAUDE.md,
	// AGENTS.md, or project memory).
	defaultPrompt = "continue"

	// maxPromptRunes bounds the resume prompt. It is passed to the sidekick on a
	// command line (capped by Windows at 32767 chars) and then typed key by key
	// into a console input buffer, so a runaway prompt would fail in two places
	// at once. Long handovers belong in a file the prompt points at.
	maxPromptRunes = 8000
)

const usage = `usage:
  self-clear        clear, then resume the fresh session with "continue"
  self-clear -      read the resume prompt from stdin`

func main() {
	args := os.Args[1:]
	if len(args) >= 1 && args[0] == "sidekick" {
		fail("sidekick", runSidekick(args[1:]))
		return
	}
	fail("self-clear", runClear(args))
}

func fail(name string, err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, name+":", err)
		os.Exit(1)
	}
}

// runClear injects /clear into the hosting Claude Code console, then spawns the
// sidekick that will prompt the fresh session once the clear has happened.
func runClear(args []string) error {
	prompt := defaultPrompt
	switch {
	case len(args) == 0:
		// No prompt. Never touches stdin, so a bare invocation can't block
		// waiting for input that will never come.
	case len(args) == 1 && args[0] == "-":
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read prompt from stdin: %w", err)
		}
		if p := normalizePrompt(string(data)); p != "" {
			prompt = p
		}
	default:
		return fmt.Errorf("unrecognized arguments %v\n%s", args, usage)
	}
	if n := len([]rune(prompt)); n > maxPromptRunes {
		return fmt.Errorf("resume prompt is %d characters, over the %d limit; "+
			"write the handover to a file and point the prompt at it instead", n, maxPromptRunes)
	}

	pid, err := coninject.FindClaudePID()
	if err != nil {
		return err
	}
	fmt.Printf("target claude pid=%d\n", pid)

	// Inject /clear in-process. coninject.Inject calls FreeConsole, which
	// detaches us from the console -- harmless here because this binary is
	// invoked via a tool whose stdout/stderr are pipes (FreeConsole leaves
	// redirected handles intact), and we exit right after. A slash command is
	// handled by the Claude Code app, not the model, so it takes effect once
	// this turn ends.
	if err := coninject.Inject(pid, clearSteps()); err != nil {
		return fmt.Errorf("inject /clear: %w", err)
	}
	fmt.Printf("injected /clear via console (pid %d).\n", pid)

	// Then spawn the windowless resume sidekick, handing it the pid we just
	// resolved (it cannot re-derive it: once we exit, the ancestry chain back to
	// claude.exe is broken) plus the prompt for the fresh session.
	if err := spawnSidekick(pid, prompt); err != nil {
		return fmt.Errorf("spawn sidekick: %w", err)
	}
	fmt.Printf("sidekick spawned (windowless, background); it will prompt the fresh session in %s with: %s\n",
		sidekickDelay, prompt)
	return nil
}

// normalizePrompt flattens a prompt to the single line the console injection can
// carry: a stray newline would submit the input early, sending half a prompt.
func normalizePrompt(s string) string {
	return strings.TrimSpace(strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(strings.TrimSpace(s)))
}

// clearSteps builds the keystroke sequence for the /clear command. /clear takes
// no arguments -- the resume prompt is the sidekick's job, since it belongs to
// the session that /clear creates, not the one it destroys.
func clearSteps() []coninject.Step {
	return []coninject.Step{
		coninject.Text("/clear"),
		coninject.Delay(enterDelay),
		coninject.Enter(),
	}
}

// spawnSidekick launches this same binary as a detached, windowless sidekick
// that will wait and then inject prompt into pid.
func spawnSidekick(pid uint32, prompt string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "sidekick", strconv.FormatUint(uint64(pid), 10), prompt)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNoWindow}
	return cmd.Start()
}

// runSidekick waits for the clear to happen, then types the resume prompt into
// the target console and exits. It detects nothing: by the time it fires, the
// old session is gone and the fresh one is sitting idle at an empty input box,
// so the prompt lands as that session's first user message. It runs windowless,
// so its only trace is the log.
func runSidekick(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: self-clear sidekick <pid> <prompt>")
	}
	pid64, err := strconv.ParseUint(args[0], 10, 32)
	if err != nil {
		return fmt.Errorf("bad pid %q: %w", args[0], err)
	}
	pid := uint32(pid64)
	prompt := args[1]

	time.Sleep(sidekickDelay)

	injErr := coninject.Inject(pid, []coninject.Step{
		coninject.Text(prompt),
		coninject.Delay(enterDelay),
		coninject.Enter(),
	})
	logSidekick(pid, prompt, injErr)
	return injErr
}

// logSidekick appends the sidekick's outcome to %TEMP%\self-clear-sidekick.log.
// The sidekick is windowless with no usable stdout/stderr, so the log file is
// the only way to see whether the resume injection succeeded.
func logSidekick(pid uint32, prompt string, injErr error) {
	path := filepath.Join(os.TempDir(), "self-clear-sidekick.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	stamp := time.Now().Format("2006-01-02 15:04:05")
	if injErr != nil {
		fmt.Fprintf(f, "%s\tpid=%d\tinject failed: %v\n", stamp, pid, injErr)
		return
	}
	fmt.Fprintf(f, "%s\tpid=%d\tinjected %q\n", stamp, pid, prompt)
}
