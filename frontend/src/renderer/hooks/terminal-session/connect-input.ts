import type { TerminalConnectEnv } from "./terminal-connect-types";
import { connectSetup } from "./connect-setup";
import { connectReplay } from "./connect-replay";
import { OPEN_TIMEOUT_MS } from "./terminal-session-constants";

export function connectInput(env: TerminalConnectEnv, setupOut: NonNullable<ReturnType<typeof connectSetup>>, replayOut: NonNullable<ReturnType<typeof connectReplay>>) {
	const {
		isCurrentAttachment,
		optionsRef,
		scheduleReattach,
		setReplaySettled,
		teardownMux,
		transition,
	} = env;
	const {
		generation,
		handle,
		mux,
		r,
		terminal,
	} = setupOut;
	const {
		flushReplay,
		initialWriteSource,
		revealReplayTail,
	} = replayOut;
	const input = terminal.onUserInput((data, source) => {
		if (!isCurrentAttachment(generation, handle, mux)) {
			return false;
		}
		// Protocol replies must bypass visibility and ownership gates so hidden panes stay connected.
		if (source === "protocol") {
			if (!r.inputReady) {
				r.queuedProtocolInputs.push(data);
				return true;
			}
			mux.sendInput(handle, data);
			return true;
		}
		if (!r.inputReady) {
			return false;
		}
		// Agent color-scheme bytes are not human input — forwarding them must not
		// flush the replay gate or reveal the tail (that was the theme-toggle jank).
		if (optionsRef.current.inputDisabled || optionsRef.current.isVisible === false) {
			return false;
		}
		// Input is accepted from `opened`, which lands before the replay — so a
		// user can type while the gate still holds the burst, and their echo
		// would sit in the buffer behind an opaque cover. Someone typing has
		// stopped caring about a tidy reveal and needs to see the pane now:
		// end the gate immediately.
		if (r.replayBuffering) flushReplay();
		else revealReplayTail();
		mux.sendInput(handle, data);
		return true;
	});
	// xterm only fires onResize when the grid actually changed. Publish that
	// grid immediately so a separator drag resizes the program while the
	// handle is still moving. The last published grid is checked because a
	// retained activation can report the same grid through several paths.
	const resize = terminal.onResize(({ cols, rows }) => {
		if (!isCurrentAttachment(generation, handle, mux)) return;
		if (optionsRef.current.isVisible === false) return;
		const published = r.lastPublishedGrid;
		if (published?.cols === cols && published.rows === rows) return;
		mux.resize(handle, cols, rows);
		r.lastPublishedGrid = { cols, rows };
	});
	r.disposers.push(
		() => input.dispose(),
		() => resize.dispose(),
	);

	// Open the replay gate before the pane can produce any output. It cannot
	// wait for `opened`: the daemon fires onOpen from setPTY and only then
	// starts copyOut (attachment.go), so `attached` arrives before the first
	// replay byte and would uncover a pane that has not drawn yet.
	// A handle this renderer just created has no history to replay: its first
	// bytes are the program starting up (a new shell's prompt). Covering them
	// only holds a blank pane through the quiet window and reveal fit, so they
	// stream straight into xterm instead.
	const coverInitialReplay = optionsRef.current.coverInitialReplay !== false && initialWriteSource !== "live";
	r.replayBuffering = coverInitialReplay;
	r.replayChunks = [];
	r.replayBytes = 0;
	setReplaySettled(!coverInitialReplay);
	// The cap is armed from `opened`, NOT from here. A slow attach — the
	// daemon runs a liveness probe and spawns the runtime client before the
	// first byte, which is why OPEN_TIMEOUT_MS budgets 3s — would otherwise
	// burn the whole cap on the handshake, flush an empty buffer, and leave
	// `replayBuffering` false for the rest of the attachment. The replay
	// would then land frame-by-frame with the bug fully intact, behind a
	// pointless blank cover. `opened` fires from setPTY immediately before
	// copyOut, so anchoring there means the cap only ever measures the burst.
	// If `opened` never arrives, the cover is lifted by the recovery path for
	// that transport: a LOCAL pane's openTimer tears down (teardownMux lifts
	// it).

	// A retained pane may reconnect while parked. It still needs the output
	// stream, but its stale off-screen grid must not resize the shared PTY.
	// Zero dimensions mean "attach without claiming a size"; the first
	// visible fit emits the authoritative grid after activation.
	// The same applies before the terminal has measured its slot: the first
	// measurement publishes the grid instead (see onVisibleSize).
	const claimsSize = optionsRef.current.isVisible !== false && terminal.hasMeasuredGrid;
	r.needsVisibleSizeSync = !claimsSize;
	const openCols = claimsSize ? terminal.cols : 0;
	const openRows = claimsSize ? terminal.rows : 0;
	mux.open(handle, openCols, openRows);
	r.lastPublishedGrid =
		openCols > 0 && openRows > 0 ? { cols: openCols, rows: openRows } : null;
	r.openTimer = setTimeout(() => {
		if (!isCurrentAttachment(generation, handle, mux)) return;
		r.openTimer = null;
		transition("reattaching");
		teardownMux();
		scheduleReattach();
	}, OPEN_TIMEOUT_MS);
}
