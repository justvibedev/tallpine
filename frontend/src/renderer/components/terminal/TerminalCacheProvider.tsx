/** Shell-owned terminal cache: retains attached terminals across route switches. */
import { useTerminalResetStore } from "../../stores/terminal-reset-store";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { type TerminalTarget } from "../../types/terminal";
import { type WorkspaceSession } from "../../types/workspace";
import type { Theme } from "../../stores/ui-store";
import { type AttachableTerminal } from "../../hooks/useTerminalSession";
import { getApiBaseUrl } from "../../lib/api-client";
import { createTerminalMux, createTerminalMuxPool, muxUrlFromApiBase, type TerminalMux, type TerminalMuxPool } from "../../lib/terminal-mux";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import { useShellTerminals } from "../../hooks/useShellTerminals";
import { CachedTerminalEntry, ActiveTerminalEntry, isUnlisted, TerminalCacheDescriptor, TerminalPaneProps, TerminalCacheController, TerminalCacheContext } from "./terminal-pane-types";
import { blurTerminal, setTerminalPhase, parkTerminal, showTerminal, terminalPropsMatch } from "./terminal-cache-helpers";
import { CachedTerminalPortal } from "./CachedTerminalPortal";

export function TerminalCacheProvider({
	children,
	daemonReady,
	theme,
}: {
	children: ReactNode;
	daemonReady: boolean;
	theme: Theme;
}) {
	const workspaceQuery = useWorkspaceQuery();
	const shellTerminalsQuery = useShellTerminals();
	const entriesRef = useRef(new Map<string, CachedTerminalEntry>());
	const activeRef = useRef<ActiveTerminalEntry | null>(null);
	const parkingRef = useRef<HTMLDivElement | null>(null);
	// The sessions the last workspace snapshot listed (null before one
	// arrived). A terminal kept on screen while its session was not listed is
	// disposed once it is parked (disposeIfUnlistedOnceParked), since a later
	// snapshot (which would dispose it then) may never come.
	const listedSessionsRef = useRef<Set<string> | null>(null);
	const muxPoolRef = useRef<TerminalMuxPool | null>(null);
	if (!muxPoolRef.current) {
		muxPoolRef.current = createTerminalMuxPool(() =>
			createTerminalMux(muxUrlFromApiBase(getApiBaseUrl())),
		);
	}
	const muxPool = muxPoolRef.current;
	const resolveCreateMux = useCallback(
		(_paneSession?: WorkspaceSession, _terminalTarget?: TerminalTarget): (() => TerminalMux) => muxPool.acquire,
		[muxPool],
	);
	const [, setRevision] = useState(0);
	const rerender = useCallback(() => setRevision((current) => current + 1), []);

	const removeEntry = useCallback(
		(cacheKey: string) => {
			const entry = entriesRef.current.get(cacheKey);
			if (!entry) return;
			const active = activeRef.current;
			if (active?.key === cacheKey) {
				blurTerminal(entry.container);
				setTerminalPhase(entry, "parked");
				activeRef.current = null;
			}
			entriesRef.current.delete(cacheKey);
			entry.container.remove();
			rerender();
		},
		[rerender],
	);

	// A terminal that left the screen while the snapshot does not list its
	// session is disposed, as the snapshot reconcile disposes a parked one: it
	// was kept only while on screen, and no later snapshot may come. The check
	// waits for the commit to finish, so a slot that re-activates the same
	// entry in it (its effect re-running) keeps it on screen without a remount.
	const disposeIfUnlistedOnceParked = useCallback(
		(cacheKey: string) => {
			queueMicrotask(() => {
				const entry = entriesRef.current.get(cacheKey);
				if (!entry || activeRef.current?.key === cacheKey) return;
				if (isUnlisted(entry, listedSessionsRef.current)) removeEntry(cacheKey);
			});
		},
		[removeEntry],
	);

	const activate = useCallback(
		(descriptor: TerminalCacheDescriptor, props: TerminalPaneProps, slot: HTMLDivElement) => {
			const parking = parkingRef.current;
			if (!parking) return;
			const cachedProps = { ...props, createMux: resolveCreateMux(props.session, props.terminalTarget) };

			const previous = activeRef.current;
			if (previous && previous.key !== descriptor.cacheKey) {
				const previousEntry = entriesRef.current.get(previous.key);
				if (previousEntry) {
					parkTerminal(previousEntry, parking);
					if (previousEntry.discardOnDeactivate) {
						entriesRef.current.delete(previous.key);
						previousEntry.container.remove();
					} else {
						disposeIfUnlistedOnceParked(previous.key);
					}
				}
			}

			// A logical terminal can have only one generation. A replacement
			// handle immediately disposes the retained renderer and writer for the
			// old generation, even if an opaque handle is later reused elsewhere.
			for (const entry of entriesRef.current.values()) {
				if (entry.ownerKey === descriptor.ownerKey && entry.cacheKey !== descriptor.cacheKey) {
					if (activeRef.current?.key === entry.cacheKey) parkTerminal(entry, parking);
					entriesRef.current.delete(entry.cacheKey);
					entry.container.remove();
				}
			}

			let entry = entriesRef.current.get(descriptor.cacheKey);
			if (!entry) {
				const container = document.createElement("div");
				// The normal terminal surface is intentionally translucent. A retained
				// terminal host must be opaque, though: during route/cache hand-off an
				// older frame can still be composited beneath it for a paint, which would
				// otherwise produce a visible double terminal.
				container.className = "h-full min-h-0 w-full overflow-hidden bg-terminal-opaque";
				container.style.position = "relative";
				container.dataset.terminalCacheKey = descriptor.cacheKey;
				entry = {
					...descriptor,
					activationId: 0,
					activationPhase: "parked",
					container,
					props: cachedProps,
				};
				entriesRef.current.set(entry.cacheKey, entry);
			} else {
				entry.props = cachedProps;
			}
			showTerminal(entry, slot);
			activeRef.current = { key: entry.cacheKey, slot };
			rerender();
		},
		[disposeIfUnlistedOnceParked, muxPool, rerender],
	);

	const deactivate = useCallback(
		(cacheKey: string, slot: HTMLDivElement) => {
			const active = activeRef.current;
			if (active?.key !== cacheKey || active.slot !== slot) return;
			const entry = entriesRef.current.get(cacheKey);
			const parking = parkingRef.current;
			activeRef.current = null;
			if (!entry) return;
			if (entry.discardOnDeactivate || !parking) {
				entriesRef.current.delete(cacheKey);
				entry.container.remove();
				rerender();
				return;
			}
			parkTerminal(entry, parking);
			disposeIfUnlistedOnceParked(cacheKey);
			rerender();
		},
		[disposeIfUnlistedOnceParked, rerender],
	);

	const update = useCallback(
		(cacheKey: string, props: TerminalPaneProps) => {
			const entry = entriesRef.current.get(cacheKey);
			const cachedProps = { ...props, createMux: resolveCreateMux(props.session, props.terminalTarget) };
			if (!entry || terminalPropsMatch(entry.props, cachedProps)) return;
			entry.props = cachedProps;
			rerender();
		},
		[muxPool, rerender],
	);

	const markFatal = useCallback(
		(cacheKey: string, _message: string) => {
			const entry = entriesRef.current.get(cacheKey);
			if (!entry) return;
			if (activeRef.current?.key !== cacheKey) {
				removeEntry(cacheKey);
				return;
			}
			// Renderer initialization failed. AttachedTerminal owns the visible
			// error surface; mark the entry only so it is discarded when the user
			// leaves instead of retaining an unusable renderer.
			entry.discardOnDeactivate = true;
		},
		[removeEntry],
	);

	const markTerminalReady = useCallback(
		(cacheKey: string, terminal: AttachableTerminal) => {
			const entry = entriesRef.current.get(cacheKey);
			if (!entry) return;
			entry.terminal = terminal;
			rerender();
		},
		[rerender],
	);

	// Daemon readiness and theme are shell-wide. Parked entries must observe
	// them too so reconnect and rendering behavior never depends on the route
	// that happened to be active when they were parked.
	useEffect(() => {
		let changed = false;
		for (const entry of entriesRef.current.values()) {
			if (entry.props.daemonReady === daemonReady && entry.props.theme === theme) continue;
			entry.props = { ...entry.props, daemonReady, theme };
			changed = true;
		}
		if (changed) rerender();
	}, [daemonReady, rerender, theme]);

	// Project/session teardown is an ownership boundary, not an LRU event.
	// Dispose retained terminal clients as soon as the authoritative workspace
	// snapshot no longer contains their logical session.
	useEffect(() => {
		if (!workspaceQuery.isSuccess) return;
		const sessions = new Map(
			(workspaceQuery.data ?? []).flatMap((workspace) =>
				workspace.sessions.map((session) => [session.id, session] as const),
			),
		);
		listedSessionsRef.current = new Set(sessions.keys());
		for (const entry of entriesRef.current.values()) {
			const session = entry.sessionId ? sessions.get(entry.sessionId) : undefined;
			if (entry.sessionId && !session) {
				// The terminal on screen is kept: its slot activates an entry only
				// when it mounts, so disposing it would leave the pane empty for as
				// long as the session stays open. A session can be on screen before
				// the snapshot lists it (opened right after it was created, or in a
				// project created elsewhere that the project list has not fetched
				// yet). Once it leaves the screen it is disposed unless a snapshot
				// lists it by then (disposeIfUnlistedOnceParked).
				if (activeRef.current?.key === entry.cacheKey) continue;
				removeEntry(entry.cacheKey);
				continue;
			}
			if (entry.kind === "worker" && session) {
				const sessionGen = session.terminalGeneration ?? "";
				const entryGen = entry.generation ?? "";
				if (session.terminalHandleId !== entry.handleId) {
					// A different handle is a different logical terminal: dispose. The
					// handle is in the cache key, so the pane re-activates a fresh
					// entry on its own.
					removeEntry(entry.cacheKey);
					continue;
				}
				if (entryGen !== "" && sessionGen !== "" && entryGen !== sessionGen) {
					// The generation moved between two known values: the terminal
					// behind this handle was relaunched. Record the new generation
					// and bump the relaunch nonce so the pane rebuilds its cache
					// entry against the live terminal.
					entry.generation = sessionGen;
					useTerminalResetStore.getState().advanceEpoch(entry.sessionId ?? session.id);
					continue;
				}
				if (entryGen === "" && sessionGen !== "") {
					// Unknown -> first-known: the worker just came online for the SAME
					// terminal. Adopt the epoch in place. The cache key does not carry
					// the epoch, so this does NOT remount the just-attached pane (which
					// would blank it: connected -> blank -> terminal).
					entry.generation = sessionGen;
				}
			}
			if (session && entry.props.session !== session) {
				entry.props = { ...entry.props, session };
				rerender();
			}
		}
	}, [removeEntry, workspaceQuery.data, workspaceQuery.isSuccess]);

	// Shell handles have their own lifecycle outside WorkspaceSession. Closing a
	// shell must close its retained mux writer even if it was parked.
	useEffect(() => {
		if (!shellTerminalsQuery.isSuccess) return;
		const shells = new Map(
			(shellTerminalsQuery.data ?? []).map((terminal) => [terminal.handleId, terminal] as const),
		);
		for (const entry of entriesRef.current.values()) {
			if (entry.kind !== "shell") continue;
			const shell = shells.get(entry.handleId);
			if (
				!shell ||
				shell.createdAt !== entry.generation ||
				shell.sessionId !== entry.sessionId
			) {
				removeEntry(entry.cacheKey);
				continue;
			}
			const target = entry.props.terminalTarget;
			if (target?.kind === "shell" && target.title !== shell.title) {
				entry.props = {
					...entry.props,
					terminalTarget: { ...target, title: shell.title },
				};
				rerender();
			}
		}
	}, [removeEntry, shellTerminalsQuery.data, shellTerminalsQuery.isSuccess]);

	// The provider is the final shell ownership boundary. React disposes the
	// portals; remove their externally-created host nodes as well.
	useEffect(
		() => () => {
			activeRef.current = null;
			for (const entry of entriesRef.current.values()) entry.container.remove();
			entriesRef.current.clear();
			muxPool.dispose();
		},
		[muxPool],
	);

	const controller = useMemo<TerminalCacheController>(
		() => ({ activate, deactivate, update, resolveCreateMux }),
		[activate, deactivate, update, resolveCreateMux],
	);

	return (
		<TerminalCacheContext.Provider value={controller}>
			<div
				ref={parkingRef}
				aria-hidden="true"
				className="pointer-events-none invisible fixed top-0 -left-[100000px]"
				data-testid="terminal-cache-parking"
			/>
			{/* The parking ref must commit before routed children run their layout
			    effects; those effects synchronously move the active container into
			    the pane slot before the browser can paint. */}
			{children}
			{[...entriesRef.current.values()].map((entry) => (
				<CachedTerminalPortal
					active={activeRef.current?.key === entry.cacheKey}
					entry={entry}
					key={entry.cacheKey}
					onFatal={markFatal}
					onTerminalReady={markTerminalReady}
				/>
			))}
		</TerminalCacheContext.Provider>
	);
}
