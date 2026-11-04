import type { TerminalConnectEnv, TerminalConnectState } from "./terminal-connect-types";
import { connectSetup } from "./connect-setup";
import { connectLive } from "./connect-live";
import { connectReplay } from "./connect-replay";
import { REPLAY_MAX_BYTES, REPLAY_QUIET_MS, REPLAY_CAP_MS, REPLAY_FIRST_BYTE_MS } from "./terminal-session-constants";

export function connectMux(env: TerminalConnectEnv, st: TerminalConnectState, setupOut: NonNullable<ReturnType<typeof connectSetup>>, liveOut: NonNullable<ReturnType<typeof connectLive>>, replayOut: NonNullable<ReturnType<typeof connectReplay>>) {
	const {
		clearOpenTimer,
		invalidateWorkspaces,
		isCurrentAttachment,
		optionsRef,
		scheduleReattach,
		setError,
		setHasAttached,
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
	const flushQueuedProtocolInputs = () => {
		if (!isCurrentAttachment(generation, handle, mux) || !r.inputReady) return;
		for (const queued of r.queuedProtocolInputs) {
			mux.sendInput(handle, queued);
		}
		r.queuedProtocolInputs = [];
	};
	const {
		drainLiveOutput,
		writeLiveOutput,
	} = liveOut;
	const {
		drainPostReplayWrites,
		flushReplay,
		initialWriteSource,
		postReplayWriteQueue,
	} = replayOut;
	r.disposers.push(
		mux.onData(handle, (bytes) => {
			if (!isCurrentAttachment(generation, handle, mux)) return;
			if (r.replayBuffering) {
				r.replayChunks.push(bytes);
				r.replayBytes += bytes.length;
				// A stream that never idles would restart the quiet window forever
				// and grow the buffer without bound; flush on size instead.
				if (r.replayBytes >= REPLAY_MAX_BYTES) {
					st.tailHoldsHistory = initialWriteSource === "replay";
					flushReplay(true);
					return;
				}
				// Each frame restarts the quiet window: the burst is over only
				// once the stream actually goes idle.
				if (r.replayQuietTimer) clearTimeout(r.replayQuietTimer);
				r.replayQuietTimer = setTimeout(() => flushReplay(true), REPLAY_QUIET_MS);
				return;
			}
			if (st.pendingReplayWrites > 0 || st.postReplayWriteActive || r.replayTailPending) {
				// Preserve wire order while the bounded initial replay is yielding,
				// and do not let a previous quiet deadline reveal before this newer
				// chunk has been parsed by xterm.
				if (r.replayTailQuietTimer) {
					clearTimeout(r.replayTailQuietTimer);
					r.replayTailQuietTimer = null;
				}
				drainLiveOutput();
				postReplayWriteQueue.push(bytes);
				drainPostReplayWrites();
				return;
			}
			writeLiveOutput(bytes);
		}),
		drainLiveOutput,
		mux.onOpened(handle, () => {
			if (!isCurrentAttachment(generation, handle, mux)) return;
			clearOpenTimer(generation);
			r.inputReady = true;
			r.attempts = 0;
			r.hasAttachedOnce = true;
			setError(undefined);
			setHasAttached(true);
			transition("attached");
			terminal.notifyCursorColorScheme();
			flushQueuedProtocolInputs();
			// Bound the gate from here: the daemon fires onOpen from setPTY and
			// starts copyOut immediately after, so the replay is imminent and
			// the cap now measures the burst rather than the connect handshake.
			if (r.replayBuffering && !r.replayCapTimer) {
				r.replayCapTimer = setTimeout(() => flushReplay(true), REPLAY_CAP_MS);
			}
			// Same anchor, different job: panes may uncover when there is
			// nothing to replay (see REPLAY_FIRST_BYTE_MS).
			if (r.replayBuffering && !r.replayFirstByteTimer) {
				r.replayFirstByteTimer = setTimeout(() => {
					r.replayFirstByteTimer = null;
					if (!isCurrentAttachment(generation, handle, mux)) return;
					// Bytes arrived, or the burst already flushed: the flush owns
					// the reveal in both cases, at the settled scroll position.
					if (!r.replayBuffering || r.replayChunks.length > 0) return;
					setReplaySettled(true);
				}, REPLAY_FIRST_BYTE_MS);
			}
		}),
		mux.onExit(handle, () => {
			if (!isCurrentAttachment(generation, handle, mux)) return;
			clearOpenTimer(generation);
			r.inputReady = false;
			// Land whatever was buffered before the notice, and lift the cover:
			// a pane that exits mid-replay must never be left behind it.
			flushReplay(false, true);
			terminal.writeln(optionsRef.current.exitNotice ?? "\r\n\x1b[2m[process exited]\x1b[0m");
			transition("exited");
			// Preserve xterm scrollback, but release the attachment: an exited
			// pane has no reason to keep a WebSocket/client writer alive.
			teardownMux();
			invalidateWorkspaces();
		}),
		mux.onError(handle, (message) => {
			if (!isCurrentAttachment(generation, handle, mux)) return;
			clearOpenTimer(generation);
			r.inputReady = false;
			flushReplay(false, true);
			terminal.writeln(`\r\n\x1b[2m[terminal error] ${message}\x1b[0m`);
			setError(message);
			transition("error");
			// Pane errors are terminal for this attachment. Keep the renderer
			// and its error text inspectable while disposing the failed socket;
			// an explicit session restore can create a fresh attachment later.
			teardownMux();
			invalidateWorkspaces();
		}),
		mux.onConnectionChange((connectionState) => {
			if (!isCurrentAttachment(generation, handle, mux)) return;
			// "closed" = a genuine socket failure (ticket minted but the
			// WebSocket dropped or never opened); "waiting" = the worker is
			// still provisioning (mint 409), which reconnects the same way but
			// must not count against the connect-failure breaker.
			if (connectionState === "closed" || connectionState === "waiting") {
				// End the gate: no replay is coming over a dead socket. This is
				// the ONLY settle path when the socket dies before `opened` —
				// clearOpenTimer below drops the open timeout, and
				// scheduleReattach schedules nothing while the daemon is down,
				// so without this the pane is stranded behind the cover with no
				// timer left to lift it (and stays there for a whole reconnect
				// storm). The cap cannot cover this: it is armed from `opened`.
				flushReplay(false, true);
				clearOpenTimer(generation);
				r.inputReady = false;
				scheduleReattach();
			}
		}),
	);
}
