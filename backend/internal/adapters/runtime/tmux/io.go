package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/adapters/runtime/inputdelay"
	"tallpine.eu/tallpine/backend/internal/adapters/runtime/ptyexec"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// SendMessage sends literal text to the session (chunked via send-keys -l) then
// presses Enter to submit. An empty message presses Enter alone (the nudge
// contract on ports.AgentMessenger).
//
// ponytail: send-keys -l chunked is simpler than load-buffer/paste-buffer; the
// ceiling is very large messages may be slower, but chunk size defaults to 16 KB
// which is ample for agent prompts.
func (r *Runtime) SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error {
	id, err := handleID(handle)
	if err != nil {
		return err
	}
	enterCtx := ctx
	if message != "" {
		delay := inputdelay.ForMessage(message, r.enterDelay)
		messageChunks := chunks(message, r.chunkSize)
		sendCtx := ctx
		var finishCancel context.CancelFunc
		for i, chunk := range messageChunks {
			if _, err := r.runForSession(sendCtx, id, sendKeysLiteralArgs(id, chunk)...); err != nil {
				if finishCancel != nil {
					finishCancel()
				}
				return fmt.Errorf("tmux runtime: send message %s: %w", id, err)
			}
			if i == 0 {
				completionBudget := sendCompletionBudget(len(messageChunks), r.timeout, delay)
				enterCtx, finishCancel = context.WithTimeout(context.WithoutCancel(ctx), completionBudget)
				sendCtx = enterCtx
			}
		}
		if finishCancel != nil {
			defer finishCancel()
		}
		// Give the target TUI a moment to accept the pasted text before the
		// trailing Enter, mirroring conpty's ptyInputEnterDelay. Without it a
		// large multiline paste can absorb the Enter and leave the prompt
		// unsubmitted (issue #2342). Empty-message nudges skip this — there is
		// no paste ahead of a catch-up Enter.
		//
		// From here on the chunks are already in the pane, so the pause and
		// the Enter are detached from the caller's cancellation (bounded by
		// their own timeout instead): abandoning mid-pause would strand an
		// unsubmitted draft that a retried send would then double-paste.
		// Errors reported by tmux after it accepts a chunk still return to the
		// caller; they are not retried because Tallpine cannot safely distinguish
		// whether tmux applied the failed command.
		if delay > 0 {
			select {
			case <-enterCtx.Done():
				return enterCtx.Err()
			case <-time.After(delay):
			}
		}
	}
	if _, err := r.runForSession(enterCtx, id, sendEnterArgs(id)...); err != nil {
		return fmt.Errorf("tmux runtime: send enter %s: %w", id, err)
	}
	return nil
}

func sendCompletionBudget(chunkCount int, commandTimeout, enterDelay time.Duration) time.Duration {
	return time.Duration(chunkCount)*commandTimeout + enterDelay
}

// Interrupt sends Ctrl-C to the foreground process without destroying the tmux
// session, keeping the terminal available for inspection and reuse.
func (r *Runtime) Interrupt(ctx context.Context, handle ports.RuntimeHandle) error {
	id, err := handleID(handle)
	if err != nil {
		return err
	}
	if _, err := r.runForSession(ctx, id, sendInterruptArgs(id)...); err != nil {
		return fmt.Errorf("tmux runtime: interrupt session %s: %w", id, err)
	}
	return nil
}

// SendInput sends raw terminal input without appending Enter. It is intended
// for TUI keybindings such as Escape rather than prompt text.
func (r *Runtime) SendInput(ctx context.Context, handle ports.RuntimeHandle, input string) error {
	id, err := handleID(handle)
	if err != nil {
		return err
	}
	args := sendKeysLiteralArgs(id, input)
	if _, err := r.runForSession(ctx, id, args...); err != nil {
		return fmt.Errorf("tmux runtime: send input %s: %w", id, err)
	}
	return nil
}

// GetOutput returns the last `lines` lines of the session pane's captured
// output.
func (r *Runtime) GetOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error) {
	id, err := handleID(handle)
	if err != nil {
		return "", err
	}
	if lines <= 0 {
		return "", errors.New("tmux runtime: lines must be positive")
	}
	out, err := r.runForSession(ctx, id, capturePaneArgs(id, lines)...)
	if err != nil {
		return "", fmt.Errorf("tmux runtime: capture output %s: %w", id, err)
	}
	return tailLines(trimTrailingBlankLines(string(out)), lines), nil
}

// GetStyledOutput is GetOutput with tmux's -e flag so SGR styling is retained.
func (r *Runtime) GetStyledOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error) {
	id, err := handleID(handle)
	if err != nil {
		return "", err
	}
	if lines <= 0 {
		return "", errors.New("tmux runtime: lines must be positive")
	}
	out, err := r.runForSession(ctx, id, capturePaneStyledArgs(id, lines)...)
	if err != nil {
		return "", fmt.Errorf("tmux runtime: capture styled output %s: %w", id, err)
	}
	return tailLines(trimTrailingBlankLines(string(out)), lines), nil
}

// Attach opens a fresh attach Stream by spawning `tmux attach-session` on a
// local PTY, sized rows x cols from birth when known. ctx cancellation closes
// the PTY.
func (r *Runtime) Attach(ctx context.Context, handle ports.RuntimeHandle, rows, cols uint16) (ports.Stream, error) {
	id, err := handleID(handle)
	if err != nil {
		return nil, err
	}
	socketName, err := r.socketForSession(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("tmux runtime: attach session %s: %w", id, err)
	}
	argv := r.attachCommandForSocket(id, socketName)
	return ptyexec.Spawn(ctx, argv, attachEnv(os.Environ()), rows, cols)
}

// attachCommand returns the argv to attach a terminal to the session.
// tmux needs no per-session env block.
//
// -u forces tmux's client-side CLIENT_UTF8 flag on. Without it, tmux infers
// UTF-8 capability from LC_ALL/LC_CTYPE/LANG in the attaching process's env
// (see tmux's main()); Tallpine's daemon is typically started without an
// interactive shell's locale, so that inference silently fails. A non-UTF8
// client makes tmux's tty_check_codeset (tty.c) replace any character it
// can't map through the legacy ACS table with underscores matching the
// glyph's display width. Box-drawing glyphs are in that ACS table so they
// still looked fine; agent CLI status icons outside it (e.g. Claude Code's
// spinner "✻" U+273B, its "⎿" U+23BF continuation marker) were silently
// rewritten to "_", which is the underscore corruption reported in #2484.
// Confirmed byte-for-byte: attaching with a stripped, locale-less env
// reproduces "_ _ _" for those glyphs; adding -u fixes it, with no observable
// difference for the still-correct box-drawing case. Tallpine already treats the
// PTY byte stream as UTF-8 end to end, so forcing the flag is always
// correct here regardless of the daemon's own environment.
func (r *Runtime) attachCommand(handle ports.RuntimeHandle) ([]string, error) {
	id, err := handleID(handle)
	if err != nil {
		return nil, err
	}
	return r.attachCommandForSocket(id, r.socketName), nil
}

func (r *Runtime) attachCommandForSocket(id, socketName string) []string {
	// The embedded xterm renderer supports 24-bit SGR colors. Tell this tmux
	// client explicitly so tmux forwards RGB instead of quantizing it to the
	// xterm-256color palette. -T is available in Tallpine's minimum tmux version (3.2).
	argv := []string{r.binaryForSocket(socketName)}
	if socketName != "" {
		argv = append(argv, "-L", socketName)
	} else if r.socketName != "" {
		// A legacy session means tmux's historical machine default, never the
		// socket named by an inherited TMUX from a Tallpine worker or nested shell.
		argv = append(argv, "-L", "default")
	}
	// Exact-match target: a plain session name falls back to tmux's unique-
	// prefix matching once this exact session is gone, which can attach the
	// client onto a different, unrelated session that happens to share the
	// prefix (see setDetachOnDestroyOnArgs). attach-session accepts the bare
	// `=<id>` form directly, unlike the pane-targeting set-option calls.
	return append(argv, "-u", "-T", "RGB", "attach-session", "-t", exactSessionTarget(id))
}

func attachEnv(base []string) []string {
	env := append([]string(nil), base...)
	hasTerm := false
	hasColorTerm := false
	for i, kv := range env {
		switch {
		case strings.HasPrefix(kv, "TERM="):
			env[i] = "TERM=xterm-256color"
			hasTerm = true
		case strings.HasPrefix(kv, "COLORTERM="):
			env[i] = "COLORTERM=truecolor"
			hasColorTerm = true
		}
	}
	if !hasTerm {
		env = append(env, "TERM=xterm-256color")
	}
	if !hasColorTerm {
		env = append(env, "COLORTERM=truecolor")
	}
	return env
}

func chunks(s string, maxBytes int) []string {
	if s == "" {
		return []string{""}
	}
	if maxBytes <= 0 || len(s) <= maxBytes {
		return []string{s}
	}
	parts := []string{}
	for s != "" {
		if len(s) <= maxBytes {
			parts = append(parts, s)
			break
		}
		end := maxBytes
		for end > 0 && !utf8.ValidString(s[:end]) {
			end--
		}
		if end == 0 {
			_, size := utf8.DecodeRuneInString(s)
			end = size
		}
		parts = append(parts, s[:end])
		s = s[end:]
	}
	return parts
}

func tailLines(s string, n int) string {
	if n <= 0 || s == "" {
		return ""
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "")
}

func trimTrailingBlankLines(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for len(lines) > 0 && strings.TrimRight(lines[len(lines)-1], "\r\n") == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "")
}

// -- env / quoting helpers --
