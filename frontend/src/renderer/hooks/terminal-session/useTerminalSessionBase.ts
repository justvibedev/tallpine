import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useRef, useState } from "react";
import { type TerminalMux } from "../../lib/terminal-mux";
import { sessionIsActive, type WorkspaceSession } from "../../types/workspace";
import { workspaceQueryKeyForHost } from "../useWorkspaceQuery";
import type { AttachableTerminal, TerminalSessionState, UseTerminalSessionOptions } from "../useTerminalSession";
import { RETRY_BASE_MS, RETRY_MAX_MS } from "./terminal-session-constants";

export function useTerminalSessionBase(session: WorkspaceSession | undefined, options: UseTerminalSessionOptions) {
	const queryClient = useQueryClient();
	const [state, setState] = useState<TerminalSessionState>("idle");
	const [error, setError] = useState<string | undefined>(undefined);
	// False only while the initial replay is being buffered — the pane keeps a
	// cover over xterm until the burst has been written and parsed.
	const [replaySettled, setReplaySettled] = useState(true);
	// True once this attachment has opened at least once. Lets the pane show a
	// calm "Connecting…" during the first connect and reserve "disconnected — reattaching" for a genuine
	// mid-session drop.
	const [hasAttached, setHasAttached] = useState(false);

	const sessionRef = useRef(session);
	sessionRef.current = session;
	const previousSessionActiveRef = useRef(session ? sessionIsActive(session) : false);
	const previousActivityStateRef = useRef(session?.activity?.state);
	const optionsRef = useRef(options);
	optionsRef.current = options;
	const stateRef = useRef<TerminalSessionState>(state);
	const connectRef = useRef<() => void>(() => undefined);

	const runtime = useRef({
		terminal: null as AttachableTerminal | null,
		mux: null as TerminalMux | null,
		handle: null as string | null,
		disposers: [] as Array<() => void>,
		retryTimer: null as ReturnType<typeof setTimeout> | null,
		openTimer: null as ReturnType<typeof setTimeout> | null,
		// Last positive grid claimed by this attachment. This is deliberately
		// separate from xterm's local grid: hidden fits must not resize the PTY, and
		// repeated identical visible fits must not manufacture another SIGWINCH.
		lastPublishedGrid: null as { cols: number; rows: number } | null,
		attempts: 0,
		generation: 0,
		inputReady: false,
		// Mirrors the hasAttached state for callbacks: false until this
		// attachment's first successful open.
		hasAttachedOnce: false,
		detached: true,
		// True only after this attachment opens parked at 0×0. The next visible
		// activation must promote it back to a positive primary grid.
		needsVisibleSizeSync: false,
		// Initial-replay gate, reset per connect (see REPLAY_QUIET_MS).
		replayBuffering: false,
		replayChunks: [] as Uint8Array[],
		replayBytes: 0,
		replayQuietTimer: null as ReturnType<typeof setTimeout> | null,
		replayCapTimer: null as ReturnType<typeof setTimeout> | null,
		// Uncovers a pane that never produced a first byte; see
		// REPLAY_FIRST_BYTE_MS. Does not end the gate, so it is not a flush timer.
		replayFirstByteTimer: null as ReturnType<typeof setTimeout> | null,
		replayTailQuietTimer: null as ReturnType<typeof setTimeout> | null,
		replayTailCapTimer: null as ReturnType<typeof setTimeout> | null,
		replayTailPending: false,
		queuedProtocolInputs: [] as string[],
		// The current attachment's flush, published so teardown can land buffered
		// bytes instead of discarding them (the closure lives inside connect).
		flushReplay: null as ((preserveBeforeTeardown?: boolean) => void) | null,
	});

	const transition = useCallback((next: TerminalSessionState) => {
		stateRef.current = next;
		setState(next);
	}, []);

	const invalidateWorkspaces = useCallback(() => {
		// A standalone shell has no session row behind it, so its exit carries no
		// news for the session board. Refetching every workspace on `exit` would
		// be pure churn — the shell terminal list owns that pane's fate instead.
		if (optionsRef.current.shellTerminalHandleId) return;
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(sessionRef.current?.hostId) });
	}, [queryClient]);

	const clearReplayTimers = useCallback(() => {
		const r = runtime.current;
		if (r.replayQuietTimer) {
			clearTimeout(r.replayQuietTimer);
			r.replayQuietTimer = null;
		}
		if (r.replayCapTimer) {
			clearTimeout(r.replayCapTimer);
			r.replayCapTimer = null;
		}
		if (r.replayFirstByteTimer) {
			clearTimeout(r.replayFirstByteTimer);
			r.replayFirstByteTimer = null;
		}
		if (r.replayTailQuietTimer) {
			clearTimeout(r.replayTailQuietTimer);
			r.replayTailQuietTimer = null;
		}
		if (r.replayTailCapTimer) {
			clearTimeout(r.replayTailCapTimer);
			r.replayTailCapTimer = null;
		}
	}, []);

	const teardownMux = useCallback(() => {
		const r = runtime.current;
		// Land anything still buffered before the attachment goes away. Dropping
		// it would lose output that had already arrived. No-ops when the gate is
		// closed or already superseded.
		r.flushReplay?.(true);
		r.flushReplay = null;
		clearReplayTimers();
		r.replayBuffering = false;
		r.replayChunks = [];
		r.replayBytes = 0;
		r.replayTailPending = false;
		r.queuedProtocolInputs = [];
		r.lastPublishedGrid = null;
		// Nothing is buffering any more, so nothing should stay covered. connect()
		// re-arms the gate immediately after calling this, in the same tick, so
		// the reveal here never flashes. Without it, a teardown that does not
		// reconnect (open timeout while the daemon is down) strands the pane
		// behind the cover — the cap no longer covers that case now it is armed
		// from `opened` rather than from connect().
		setReplaySettled(true);
		if (r.retryTimer) {
			clearTimeout(r.retryTimer);
			r.retryTimer = null;
		}
		if (r.openTimer) {
			clearTimeout(r.openTimer);
			r.openTimer = null;
		}
		r.inputReady = false;
		if (r.mux && r.handle) {
			r.mux.close(r.handle);
		}
		r.disposers.forEach((dispose) => dispose());
		r.disposers = [];
		r.mux?.dispose();
		r.mux = null;
	}, [clearReplayTimers]);

	const isCurrentAttachment = useCallback((generation: number, handle: string, mux: TerminalMux) => {
		const r = runtime.current;
		return !r.detached && r.generation === generation && r.handle === handle && r.mux === mux;
	}, []);

	const clearOpenTimer = useCallback((generation: number) => {
		const r = runtime.current;
		if (r.generation !== generation || !r.openTimer) return;
		clearTimeout(r.openTimer);
		r.openTimer = null;
	}, []);

	const scheduleReattach = useCallback(() => {
		const r = runtime.current;
		if (r.detached || !r.terminal || !r.handle) {
			return;
		}
		// A socket dropping after the PTY ended (or errored) changes nothing.
		if (stateRef.current === "exited" || stateRef.current === "error") {
			return;
		}
		transition("reattaching");
		// Not ready → no timer; the daemonReady effect reconnects when it flips.
		if (!optionsRef.current.daemonReady) {
			return;
		}
		if (r.retryTimer) {
			return;
		}
		const delay = Math.min(RETRY_BASE_MS * 2 ** r.attempts, RETRY_MAX_MS);
		r.attempts += 1;
		r.retryTimer = setTimeout(() => {
			r.retryTimer = null;
			connectRef.current();
		}, delay);
	}, [transition]);

	return {
		clearOpenTimer,
		clearReplayTimers,
		connectRef,
		error,
		hasAttached,
		invalidateWorkspaces,
		isCurrentAttachment,
		optionsRef,
		previousActivityStateRef,
		previousSessionActiveRef,
		replaySettled,
		runtime,
		scheduleReattach,
		sessionRef,
		setError,
		setHasAttached,
		setReplaySettled,
		state,
		stateRef,
		teardownMux,
		transition,
	};
}
