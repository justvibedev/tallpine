// Terminal Attachment (see CONTEXT.md): the live binding between a terminal
// pane and a PTY over the mux. The hook owns the whole attachment lifecycle —
// open ordering, auto-reattach with backoff, error surfacing, and exit
// handling — so the pane component only renders.
//
// The PTY is either an agent session's pane (the default) or a standalone
// shell terminal the user opened by hand (options.shellTerminalHandleId). The
// mux draws no distinction between them, so only the handle's source and the
// session-specific side effects branch below.
//
// Status rule: the frontend never writes a session's display status. On mux
// `exited`/`error` it invalidates the workspaces query and lets the daemon's
// derived status flow back (docs/architecture.md).

import { useCallback, useEffect, useLayoutEffect } from "react";
import { type TerminalMux } from "../lib/terminal-mux";
import { sessionIsActive, type WorkspaceSession } from "../types/workspace";

/**
 * The slice of xterm's Terminal the attachment needs. Structural, so tests can
 * drive the hook with a tiny fake instead of a real xterm + DOM.
 */
export type TerminalUserInputSource = "keyboard" | "paste" | "composition" | "shortcut" | "wheel" | "protocol";
export type TerminalWriteSource = "live" | "replay";

export type AttachableTerminal = {
	cols: number;
	rows: number;
	/**
	 * False until cols/rows come from measuring the terminal's laid-out slot.
	 * Before that they are xterm's constructor default, which must never be
	 * claimed as the PTY's size: a shell started at it lays out its first prompt
	 * for the wrong width.
	 */
	hasMeasuredGrid: boolean;
	/**
	 * `done` fires once this exact chunk has been parsed into the buffer (xterm's
	 * own write callback). The attachment uses it to reveal the pane at the
	 * replay's final scroll position instead of guessing with a timer.
	 */
	write: (data: Uint8Array, done?: () => void, source?: TerminalWriteSource) => void;
	writeln: (line: string) => void;
	/** Move xterm's logical viewport and DOM scrollbar to the latest output. */
	showLatestOutput: () => void;
	/**
	 * Fit a retained terminal and move it to the latest output while its
	 * container is still non-visible. Resolves only after xterm has rendered the
	 * bottom viewport and crossed a paint boundary, so the owner can reveal
	 * without exposing an intermediate row.
	 */
	prepareForActivation: () => Promise<void>;
	/**
	 * Restore the caret after the owner re-activates a retained terminal (tab
	 * switch back to this pane). Must stay guarded: it may not steal focus from
	 * dialogs or other controls that legitimately hold it.
	 */
	requestActivationFocus: () => void;
	/** Tell Cursor Agent the live light/dark scheme (private 997 notification). */
	notifyCursorColorScheme: () => void;
	/** Send an explicit UI action through the same guarded path as user input. */
	sendUserInput: (data: string, source?: TerminalUserInputSource) => boolean;
	onUserInput: (listener: (data: string, source: TerminalUserInputSource) => boolean | void) => { dispose: () => void };
	onResize: (listener: (size: { cols: number; rows: number }) => void) => { dispose: () => void };
};

export type TerminalSessionState =
	| "idle" // nothing attached (no session, or detached)
	| "connecting" // first attach in flight
	| "attached" // server acked the open
	| "reattaching" // socket dropped; waiting on backoff or daemon readiness
	| "exited" // PTY process ended; terminal kept for scrollback
	| "error"; // server reported a pane error; no automatic retry

export type UseTerminalSessionOptions = {
	/** Gates auto-reattach: when false, a dropped socket waits instead of retrying. */
	daemonReady: boolean;
	/** Refuse user bytes without detaching while a controller handoff owns input. */
	inputDisabled?: boolean;
	/** Coalesce and cover the initial replay. Disable for non-retained reviewer panes. */
	coverInitialReplay?: boolean;
	/**
	 * False while a retained terminal is parked off screen. Output and transport
	 * recovery continue, but hidden panes cannot send user input or PTY resizes.
	 */
	isVisible?: boolean;
	/** Test seam: build the mux client. Defaults to a fresh socket against the current API base. */
	createMux?: () => TerminalMux;
	/** Contextual terminal-close text for an intentionally short-lived pane. */
	exitNotice?: string;
	/**
	 * Attach to a standalone shell terminal (POST /api/v1/shell-terminals)
	 * instead of a session's pane. When set it wins over `session`, which
	 * callers pass as undefined for shell panes.
	 *
	 * The mux needs no distinction between the two: it treats the id it is
	 * given as an opaque runtime handle either way. Everything downstream of
	 * `handle` in this hook is therefore shared verbatim; only the handle's
	 * source and the session-specific side effects differ.
	 */
	shellTerminalHandleId?: string;
};


import { useTerminalSessionBase } from "./terminal-session/useTerminalSessionBase";
import { useTerminalConnect } from "./terminal-session/useTerminalConnect";

export function useTerminalSession(session: WorkspaceSession | undefined, options: UseTerminalSessionOptions) {
	const base = useTerminalSessionBase(session, options);
	const connect = useTerminalConnect(base);
	const {
		error,
		hasAttached,
		optionsRef,
		previousActivityStateRef,
		previousSessionActiveRef,
		replaySettled,
		runtime,
		sessionRef,
		setError,
		setHasAttached,
		setReplaySettled,
		state,
		stateRef,
		teardownMux,
		transition,
	} = base;

	/**
	 * Bind a terminal to the current session's PTY. Call once the terminal is
	 * opened (and fitted); returns the detach function for effect cleanup.
	 */
	const attach = useCallback(
		(terminal: AttachableTerminal) => {
			const r = runtime.current;
			const handle = optionsRef.current.shellTerminalHandleId ?? sessionRef.current?.terminalHandleId ?? null;
			r.terminal = terminal;
			r.handle = handle;
			r.detached = false;
			r.attempts = 0;
			r.hasAttachedOnce = false;
			setError(undefined);
			setHasAttached(false);
			if (handle) {
				if (optionsRef.current.daemonReady) {
					transition("connecting");
					connect();
				} else {
					transition("reattaching");
				}
			} else {
				transition("idle");
			}
			return () => {
				// Before the generation bump — past it the flush's own guard rejects
				// it and the buffered bytes (and any URL in them) are lost. This is
				// the session-switch path, so it is the one that matters most.
				r.flushReplay?.(true);
				r.generation += 1;
				r.detached = true;
				teardownMux();
				r.terminal = null;
				r.handle = null;
				r.inputReady = false;
				r.needsVisibleSizeSync = false;
				setError(undefined);
				// Detaching ends any pending replay: never leave the next mount of
				// this hook believing a burst is still in flight.
				setReplaySettled(true);
				transition("idle");
			};
		},
		[connect, teardownMux, transition],
	);

	// Publish the retained terminal's positive grid only after activation has
	// painted it and made it visible. A parked reconnect opens at 0×0, while a
	// continuously connected parked terminal may be refitted locally with resize
	// forwarding suppressed. In both cases activation must explicitly promote the
	// retained primary back to an authoritative size.
	const syncVisibleSize = useCallback((cols: number, rows: number) => {
		const r = runtime.current;
		if (
			optionsRef.current.isVisible === false ||
			r.detached ||
			!r.mux ||
			!r.handle ||
			!r.needsVisibleSizeSync ||
			cols <= 0 ||
			rows <= 0
		) {
			return;
		}
		r.needsVisibleSizeSync = false;
		const published = r.lastPublishedGrid;
		if (published?.cols === cols && published.rows === rows) return;
		r.mux.resize(r.handle, cols, rows);
		r.lastPublishedGrid = { cols, rows };
	}, []);

	// Daemon came back while we were waiting: reconnect immediately, without
	// backoff debt from attempts made against the dead daemon.
	const daemonReady = options.daemonReady;
	useEffect(() => {
		const r = runtime.current;
		if (!daemonReady || r.detached) return;
		if (stateRef.current !== "reattaching" || r.retryTimer) return;
		r.attempts = 0;
		connect();
	}, [daemonReady, connect]);

	// A parked cache entry keeps parsing output, but it must be inert as a PTY
	// client. A hidden local refit cannot be forwarded. useLayoutEffect runs
	// before the cache's activation preparation, so the first visible frame
	// always publishes its final positive grid even when xterm's local size no
	// longer changes.
	// Tell the daemon nobody is looking at this terminal. A 0×0 grid is the mux's
	// "attached without claiming a size": the daemon stops counting this client
	// as showing the terminal (so an idle agent may be restarted on a new harness
	// release) and leaves the PTY at its size unless another client shows it.
	// Forgetting the published grid makes the next visible sync send the real
	// one again. (Ported from
	// Account Manager, #6274.)
	const parkOffScreen = useCallback(() => {
		const r = runtime.current;
		r.needsVisibleSizeSync = true;
		if (!r.mux || !r.handle || !r.lastPublishedGrid) return;
		r.mux.resize(r.handle, 0, 0);
		r.lastPublishedGrid = null;
	}, []);

	const isVisible = options.isVisible !== false;
	useLayoutEffect(() => {
		if (!isVisible) parkOffScreen();
	}, [isVisible, parkOffScreen]);

	// A hidden or minimised window takes its active pane off screen as well.
	useEffect(() => {
		const onWindowVisibility = () => {
			const terminal = runtime.current.terminal;
			if (document.visibilityState === "hidden") parkOffScreen();
			else if (terminal?.hasMeasuredGrid) syncVisibleSize(terminal.cols, terminal.rows);
		};
		document.addEventListener("visibilitychange", onWindowVisibility);
		return () => document.removeEventListener("visibilitychange", onWindowVisibility);
	}, [parkOffScreen, syncVisibleSize]);

	useEffect(() => {
		const r = runtime.current;
		const handle = session?.terminalHandleId ?? null;
		const isActive = session ? sessionIsActive(session) : false;
		const wasActive = previousSessionActiveRef.current;
		const previousActivityState = previousActivityStateRef.current;
		previousSessionActiveRef.current = isActive;
		previousActivityStateRef.current = session?.activity?.state;
		const restoredSession = !wasActive && isActive;
		const resumedAgent = previousActivityState === "exited" && session?.activity?.state !== "exited";
		if (!handle || (!restoredSession && !resumedAgent) || r.detached || !r.terminal) {
			return;
		}
		if (r.handle !== handle) return;
		if (stateRef.current !== "exited" && stateRef.current !== "error") return;
		if (optionsRef.current.daemonReady) {
			transition("connecting");
			connect();
		} else {
			transition("reattaching");
		}
	}, [
		connect,
		session?.activity?.state,
		session?.isTerminated,
		session?.status,
		session?.terminalHandleId,
		transition,
	]);

	// Belt-and-braces: never leak a socket past unmount, even if the owner
	// forgot to call detach.
	useEffect(
		() => () => {
			const r = runtime.current;
			// Same ordering rule as the detach path above.
			r.flushReplay?.(true);
			r.generation += 1;
			r.detached = true;
			r.inputReady = false;
			teardownMux();
		},
		[teardownMux],
	);

	useEffect(() => {
		if (!replaySettled) return;
		runtime.current.terminal?.notifyCursorColorScheme();
	}, [replaySettled]);

	return { attach, state, error, replaySettled, hasAttached, syncVisibleSize };
}
