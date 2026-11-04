import type { XtermWebViewHandle } from "@fressh/react-native-xtermjs-webview";
import { useCallback, useLayoutEffect, useMemo, useEffect, useRef, useState } from "react";
import { isConfigured, type ServerConfig } from "../config";
import { MuxClient, type MuxStatus } from "../mux";
import { isTerminalGoneError, terminalErrorCopy, terminalExitedCopy } from "./terminalCopy";

/**
 * The mux socket, the xterm WebView handle and the grid bookkeeping between
 * them. Owns the connection status, the rendered grid size, the banner and the
 * "terminal is gone" flag the screen reads.
 */
export function useTerminalBridge({ activeConfig, terminalHandleId, projectId, scheme, style }: {
	activeConfig: ServerConfig | null | undefined;
	terminalHandleId: string;
	projectId: string | undefined;
	scheme: unknown;
	style: unknown;
}) {
	const xtermRef = useRef<XtermWebViewHandle | null>(null);
	// Theme changes still replace xterm. Retain bytes arriving between the old
	// WebView unmount and the replacement's onInitialized callback in wire order.
	const xtermReadyRef = useRef(false);
	const pendingOutputRef = useRef<Uint8Array[]>([]);
	const muxRef = useRef<MuxClient | null>(null);
	const openedRef = useRef(false);
	// Last grid size reported by the WebView's FitAddon, so we can send it to the
	// PTY the moment the terminal opens (dims may arrive before or after open).
	const lastDimsRef = useRef<{ cols: number; rows: number } | null>(null);
	// The authoritative grid the daemon told us the shared PTY is actually using
	// (driven by the largest/primary client — e.g. a co-viewing desktop). We render
	// THIS grid, not the phone's own fit, so the display matches the PTY and a
	// full-screen TUI doesn't mis-render. The WebView crops/scales it locally.
	const authRef = useRef<{ cols: number; rows: number } | null>(null);


	const [status, setStatus] = useState<MuxStatus>("connecting");
	const [size, setSize] = useState<{ cols: number; rows: number } | null>(null);
	const [banner, setBanner] = useState<string | null>(null);
	// A terminated session has no live PTY (the mux answers "Session not found").
	// Track that + the known status so we can offer Restore instead of a dead term.
	const [notFound, setNotFound] = useState(false);

	// Load config, then connect the mux socket.
	// Rebuilt whenever the active endpoint changes, not only when the session
	// does. The app re-races its endpoints when the network moves — losing
	// Wi-Fi hands the session to Tailscale or the tunnel — and a mux still
	// pointed at the previous address stays disconnected on a blank screen
	// until the screen is closed and reopened.
	useEffect(() => {
		let disposed = false;
		(async () => {
			// A selected machine without a verified connection has no terminal.
			// The global saved config may belong to a different machine.
			const config = activeConfig;
			if (disposed) return;
			if (!config || !isConfigured(config)) return;

			const mux = new MuxClient(config, {
				onStatus: (s) => setStatus(s),
				onTerminalData: (tid, bytes) => {
					if (tid !== terminalHandleId) return;
					if (!xtermReadyRef.current || !xtermRef.current) {
						pendingOutputRef.current.push(bytes.slice());
						return;
					}
					xtermRef.current.write(bytes);
				},
				onTerminalExited: (tid, code) => {
					if (tid === terminalHandleId) {
						setBanner(terminalExitedCopy(code));
						setNotFound(true);
					}
				},
				onTerminalError: (tid, msg) => {
					if (tid !== terminalHandleId) return;
					// A missing PTY means the session is terminated - offer Restore
					// instead of surfacing it as a raw error banner.
					if (isTerminalGoneError(msg)) setNotFound(true);
					else setBanner(terminalErrorCopy(msg));
				},
				onTerminalResize: (tid, cols, rows) => {
					if (tid !== terminalHandleId) return;
					// Render the daemon's authoritative grid exactly. The WebView applies
					// only local crop/zoom, so the phone cannot disturb a desktop owner.
					authRef.current = { cols, rows };
					setSize({ cols, rows });
					xtermRef.current?.resize({ cols, rows });
				},
			});
			muxRef.current = mux;
			mux.connect();
		})();
		return () => {
			disposed = true;
			muxRef.current?.disconnect();
			muxRef.current = null;
			xtermReadyRef.current = false;
			pendingOutputRef.current = [];
		};
		// The store preserves config identity when the endpoint and credential
		// did not change, so this also handles two hosts sharing one URL.
	}, [terminalHandleId, activeConfig]);

	useLayoutEffect(() => {
		xtermReadyRef.current = false;
	}, [scheme, style]);

	// The WebView reports the phone's NATURAL fit (proposeDimensions, measure-only).
	// We forward it to the daemon as this client's requested size — used only when
	// the phone is the sole viewer (a co-viewing desktop, being primary, wins). The
	// render grid comes back via onTerminalResize; until it does, render the fit so
	// the terminal isn't blank.
	const applyDims = useCallback(
		(cols: number, rows: number) => {
			lastDimsRef.current = { cols, rows };
			if (openedRef.current) muxRef.current?.resize(terminalHandleId, cols, rows, projectId);
			if (!authRef.current) {
				setSize({ cols, rows });
				xtermRef.current?.resize({ cols, rows });
			}
		},
		[terminalHandleId, projectId],
	);

	// fressh routes WebView {type:'debug'} messages to logger.log(prefix, message).
	// We piggyback on it for the FRESSH_DIMS report (using a custom onMessage would
	// clobber fressh's own bridge).
	const logger = useMemo(
		() => ({
			log: (...args: unknown[]) => {
				const m = args[args.length - 1];
				if (typeof m === "string" && m.startsWith("FRESSH_DIMS ")) {
					const parts = m.split(" ");
					const cols = parseInt(parts[1], 10);
					const rows = parseInt(parts[2], 10);
					if (cols > 0 && rows > 0) applyDims(cols, rows);
				}
			},
		}),
		[applyDims],
	);

	const onInitialized = useCallback(() => {
		xtermReadyRef.current = true;
		const pending = pendingOutputRef.current;
		pendingOutputRef.current = [];
		for (const bytes of pending) xtermRef.current?.write(bytes);
		// A fresh xterm (first mount, or a remount after a font-zoom) starts at its
		// default grid — restore the daemon's authoritative grid onto it so it keeps
		// mirroring the shared PTY rather than snapping to the default.
		if (authRef.current) xtermRef.current?.resize(authRef.current);
		// Guard against a second open if the WebView re-fires onInitialized (e.g.
		// remount on orientation change) - that would attach the PTY twice.
		if (openedRef.current) return;
		openedRef.current = true;
		muxRef.current?.openTerminal(terminalHandleId, projectId);
		// If the FitAddon already reported dims before open, send them to the PTY now.
		const d = lastDimsRef.current;
		if (d) muxRef.current?.resize(terminalHandleId, d.cols, d.rows, projectId);
	}, [terminalHandleId, projectId]);

	const onData = useCallback(
		(data: string) => {
			muxRef.current?.sendInput(terminalHandleId, data, projectId);
		},
		[terminalHandleId, projectId],
	);

	const sendKey = useCallback(
		(seq: string) => {
			muxRef.current?.sendInput(terminalHandleId, seq, projectId);
		},
		[terminalHandleId, projectId],
	);
	return {
		xtermRef, muxRef, openedRef, lastDimsRef,
		status, size, banner, setBanner, notFound, setNotFound,
		logger, onInitialized, onData, sendKey,
	};
}
