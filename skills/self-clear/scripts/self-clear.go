// Command self-clear injects Claude Code's /clear slash command into the
// terminal hosting the current session, then spawns a detached, windowless
// "sidekick" that types a resume prompt into the fresh session a few seconds
// later -- so an autonomous workflow keeps going after its context is wiped.
//
// It is meant to be run straight from source, from this directory:
//
//	go run .            clear, then resume the fresh session with "continue"
//	go run . -          read the resume prompt from stdin
//	go run . --dry-run  report what would be injected, and inject nothing
//
// One internal form, used only when the program re-launches itself:
//
//	self-clear-sidekick.exe sidekick <pid> <prompt>
//
// Injection is focus-free: the program walks its own process ancestry to the
// hosting claude.exe, AttachConsole()s to it, and WriteConsoleInputW()s straight
// into that console's input buffer. Only the Windows standard library is used,
// so `go run` never has to fetch a module or a newer toolchain.
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
	"unsafe"
)

const (
	// Windows CreateProcess flags. DETACHED_PROCESS gives the child no console
	// at all; CREATE_NO_WINDOW suppresses a window for any console it might
	// otherwise get. Together, on a native console-subsystem exe, the sidekick
	// runs truly windowless.
	detachedProcess = 0x00000008
	createNoWindow  = 0x08000000

	// enterDelay is the pause between typing a line and pressing Enter, long
	// enough for Claude Code's input to register the text first.
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

	// clearCommand is the slash command typed into the hosting console. It takes
	// no arguments -- the resume prompt is the sidekick's job, since it belongs
	// to the session /clear creates, not the one it destroys.
	clearCommand = "/clear"

	// sidekickExe is the name of the stable copy this program spawns as its
	// sidekick. See sidekickBinary for why a copy is needed.
	sidekickExe = "self-clear-sidekick.exe"

	// sidekickLog is the sidekick's log, kept beside it in dataDir.
	sidekickLog = "sidekick.log"
)

const usage = `usage (run from this directory):
  go run .            clear, then resume the fresh session with "continue"
  go run . -          read the resume prompt from stdin
  go run . --dry-run  report what would be injected, and inject nothing
                      (combines with the forms above)`

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
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Println(usage)
		return nil
	}

	var dryRun bool
	rest := args
	if len(rest) > 0 && rest[0] == "--dry-run" {
		dryRun, rest = true, rest[1:]
	}

	prompt := defaultPrompt
	switch {
	case len(rest) == 0:
		// No prompt. Never touches stdin, so a bare invocation cannot block
		// waiting for input that will never come.
	case len(rest) == 1 && rest[0] == "-":
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

	pid, err := findClaudePID()
	if err != nil {
		return err
	}
	fmt.Printf("target claude pid=%d\n", pid)

	if dryRun {
		fmt.Printf("dry run: would submit %s to pid %d, then spawn a sidekick to prompt the fresh session in %s with: %s\n",
			clearCommand, pid, sidekickDelay, prompt)
		fmt.Println("nothing was injected.")
		return nil
	}

	// Inject /clear in-process. injectLine calls FreeConsole, which detaches us
	// from the console -- harmless here because this program is invoked via a
	// tool whose stdout/stderr are pipes (FreeConsole leaves redirected handles
	// intact), and we exit right after. A slash command is handled by the Claude
	// Code app, not the model, so it queues and fires once this turn ends.
	if err := injectLine(pid, clearCommand); err != nil {
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

// runSidekick waits for the clear to happen, then types the resume prompt into
// the target console and exits. It detects nothing: by the time it fires, the
// old session is gone and the fresh one is sitting idle at an empty input box,
// so the prompt lands as that session's first user message. It runs windowless,
// so its only trace is the log.
func runSidekick(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: %s sidekick <pid> <prompt>", sidekickExe)
	}
	pid64, err := strconv.ParseUint(args[0], 10, 32)
	if err != nil {
		return fmt.Errorf("bad pid %q: %w", args[0], err)
	}
	pid := uint32(pid64)
	prompt := args[1]

	time.Sleep(sidekickDelay)

	injErr := injectLine(pid, prompt)
	logSidekick(pid, prompt, injErr)
	return injErr
}

// logSidekick appends the sidekick's outcome to its log in dataDir. The sidekick
// is windowless with no usable stdout/stderr, so the log file is the only way to
// see whether the resume injection succeeded.
func logSidekick(pid uint32, prompt string, injErr error) {
	dir, err := dataDir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, sidekickLog)
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

// --- Spawning the sidekick --------------------------------------------------

// spawnSidekick launches a detached, windowless copy of this program that will
// wait and then inject prompt into pid.
func spawnSidekick(pid uint32, prompt string) error {
	exe, err := sidekickBinary()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "sidekick", strconv.FormatUint(uint64(pid), 10), prompt)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNoWindow}
	return cmd.Start()
}

// dataDir is where self-clear keeps the two files it owns: the stable copy of
// this executable that the sidekick runs from, and the sidekick's log.
//
// os.UserCacheDir() is %LOCALAPPDATA% on Windows, the conventional per-user,
// per-machine home for a program's own files. Deliberately not a temp
// directory: the log is the only record of whether the resume injection worked,
// and it should still be there when someone goes looking for it.
func dataDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "self-clear")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// sidekickBinary returns a path to this program's executable that will outlive
// this process, copying the running image there if needed.
//
// The copy is the price of `go run`. Whenever the source has changed it links
// into a temporary directory and deletes that directory as soon as the program
// exits; a sidekick launched from there would hold the file open, so the cleanup
// would fail ("Access is denied") and `go run` would exit non-zero even though
// the injection worked. On an unchanged source Go instead runs the executable
// out of its build cache, where it does persist -- but there is no way from here
// to tell the two apart, and the build cache is not somewhere to spawn from
// regardless. Spawning from a stable copy sidesteps both.
func sidekickBinary() (string, error) {
	src, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir, err := dataDir()
	if err != nil {
		return "", err
	}

	dst := filepath.Join(dir, sidekickExe)
	if err := copyFile(src, dst); err != nil {
		// A sidekick from a recent invocation is still running and holds the
		// file open. That copy is this same program, so reuse it as it is.
		if _, statErr := os.Stat(dst); statErr == nil {
			return dst, nil
		}
		return "", fmt.Errorf("copy sidekick binary to %s: %w", dst, err)
	}
	return dst, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// --- Console keystroke injection --------------------------------------------
//
// Text is delivered by attaching to the target console and writing straight into
// its input buffer, which bypasses the windowing/UIPI layer entirely: no window
// focus, foreground switch, IME or clipboard is involved. That is also why it
// works across the normal->elevated integrity boundary, in the common case where
// the elevated console was launched by the same interactive user.

const (
	keyEvent  = 0x0001
	vkReturn  = 0x0D
	genericRW = syscall.GENERIC_READ | syscall.GENERIC_WRITE
	shareRW   = syscall.FILE_SHARE_READ | syscall.FILE_SHARE_WRITE
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	procFreeConsole       = kernel32.NewProc("FreeConsole")
	procAttachConsole     = kernel32.NewProc("AttachConsole")
	procWriteConsoleInput = kernel32.NewProc("WriteConsoleInputW")
)

// keyEventRecord mirrors KEY_EVENT_RECORD. Field order and sizes must match the
// C struct exactly so the bytes land correctly in WriteConsoleInputW.
type keyEventRecord struct {
	bKeyDown          int32 // BOOL
	wRepeatCount      uint16
	wVirtualKeyCode   uint16
	wVirtualScanCode  uint16
	unicodeChar       uint16
	dwControlKeyState uint32
}

// inputRecord mirrors INPUT_RECORD for a KEY_EVENT. The uint16 padding after
// eventType aligns the union (which starts with a 4-byte BOOL) to a 4-byte
// boundary, matching the C layout (20 bytes total).
type inputRecord struct {
	eventType uint16
	_         uint16
	keyEvent  keyEventRecord
}

// injectLine types s into the console owned by targetPID, pauses for the input
// to register, then presses Enter to submit it.
//
// The calling process must NOT depend on its own console afterward: FreeConsole
// tears down stdout/stderr. Run this in a short-lived or detached process, or in
// one whose stdout/stderr are redirected to pipes or files, which FreeConsole
// leaves intact.
func injectLine(targetPID uint32, s string) error {
	// A process can be attached to only one console; release ours first.
	procFreeConsole.Call()

	r, _, err := procAttachConsole.Call(uintptr(targetPID))
	if r == 0 {
		return fmt.Errorf("AttachConsole(%d) failed: %w", targetPID, err)
	}
	defer procFreeConsole.Call()

	name, err := syscall.UTF16PtrFromString("CONIN$")
	if err != nil {
		return err
	}
	handle, err := syscall.CreateFile(name, genericRW, shareRW, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		return fmt.Errorf("open CONIN$: %w", err)
	}
	defer syscall.CloseHandle(handle)

	var recs []inputRecord
	for _, ch := range s {
		recs = append(recs, keyRecords(uint16(ch), 0)...)
	}
	if err := writeRecords(handle, recs); err != nil {
		return fmt.Errorf("type %q: %w", s, err)
	}

	time.Sleep(enterDelay)
	if err := writeRecords(handle, keyRecords('\r', vkReturn)); err != nil {
		return fmt.Errorf("press Enter: %w", err)
	}
	return nil
}

// keyRecords returns the down+up INPUT_RECORD pair for a single character.
// Pass vk=0 for ordinary printable text, or a virtual-key code (e.g. vkReturn).
func keyRecords(ch uint16, vk uint16) []inputRecord {
	recs := make([]inputRecord, 0, 2)
	for _, down := range []bool{true, false} {
		var b int32
		if down {
			b = 1
		}
		recs = append(recs, inputRecord{
			eventType: keyEvent,
			keyEvent: keyEventRecord{
				bKeyDown:        b,
				wRepeatCount:    1,
				wVirtualKeyCode: vk,
				unicodeChar:     ch,
			},
		})
	}
	return recs
}

func writeRecords(handle syscall.Handle, recs []inputRecord) error {
	if len(recs) == 0 {
		return nil
	}
	var written uint32
	r, _, err := procWriteConsoleInput.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&recs[0])),
		uintptr(len(recs)),
		uintptr(unsafe.Pointer(&written)),
	)
	if r == 0 {
		return fmt.Errorf("WriteConsoleInputW failed: %w", err)
	}
	if int(written) != len(recs) {
		return fmt.Errorf("WriteConsoleInputW wrote %d of %d records", written, len(recs))
	}
	return nil
}

// --- Finding the target console ---------------------------------------------
//
// Walking the parent chain, rather than enumerating windows, is what keeps the
// injection focus-free and elevation-tolerant.

type procRow struct {
	ppid uint32
	name string
}

func snapshotProcs() (map[uint32]procRow, error) {
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("CreateToolhelp32Snapshot: %w", err)
	}
	defer syscall.CloseHandle(snap)

	var e syscall.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	if err := syscall.Process32First(snap, &e); err != nil {
		return nil, fmt.Errorf("Process32First: %w", err)
	}

	procs := make(map[uint32]procRow, 256)
	for {
		procs[e.ProcessID] = procRow{
			ppid: e.ParentProcessID,
			name: syscall.UTF16ToString(e.ExeFile[:]),
		}
		if err := syscall.Process32Next(snap, &e); err != nil {
			break
		}
	}
	return procs, nil
}

// findClaudePID walks the parent chain from the current process up to the first
// claude.exe ancestor and returns its PID. This program runs as a descendant of
// the Claude Code session (claude.exe -> shell -> go run -> self-clear), so that
// ancestor is the console-owning process to inject into.
//
// It must be called from the still-living main process: once that process exits
// and a detached child is reparented, the chain back to claude.exe is broken,
// which is why the resolved PID is passed to the sidekick rather than re-derived.
func findClaudePID() (uint32, error) {
	procs, err := snapshotProcs()
	if err != nil {
		return 0, err
	}
	pid := uint32(os.Getpid())
	for i := 0; i < 64; i++ {
		row, ok := procs[pid]
		if !ok {
			break
		}
		if strings.EqualFold(row.name, "claude.exe") {
			return pid, nil
		}
		pid = row.ppid
	}
	return 0, fmt.Errorf("no claude.exe ancestor found (is this running inside a Claude Code session?)")
}
