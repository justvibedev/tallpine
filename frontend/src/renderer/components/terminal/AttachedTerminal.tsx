/** The attached xterm surface with PTY streaming, banners and restore controls. */
import { PRODUCT_NAME } from "../../../shared/brand";
import { useQueryClient } from "@tanstack/react-query";
import { Suspense, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { sessionAgentExited, sessionIsActive } from "../../types/workspace";
import { useTerminalSession, type AttachableTerminal, type TerminalSessionState } from "../../hooks/useTerminalSession";
import { useSessionBrowserLink } from "../../hooks/useSessionBrowserLink";
import { useSessionLinkNavigation } from "../../lib/use-session-link-navigation";
import { workspaceQueryKeyForHost } from "../../hooks/useWorkspaceQuery";
import { useRestoreSession } from "../../hooks/useRestoreSession";
import { LazyChunkBoundary } from "../LazyChunkBoundary";
import { RestoreUnavailableDialog } from "../RestoreUnavailableDialog";
import type { TerminalPaneProps } from "./terminal-pane-types";
import { TerminalEndedStrip, ReplayCover } from "./TerminalEndedStrip";
import { TerminalBootStatus } from "./TerminalBootStatus";
import { XtermTerminal } from "./xterm-lazy";

export const KEYBOARD_SCROLL_PROVIDERS = new Set<string>();

// Whether the given provider's TUI is one of the keyboard-scroll agents above.
export function providerScrollsByKeyboard(provider?: string): boolean {
	return provider ? KEYBOARD_SCROLL_PROVIDERS.has(provider) : false;
}

export function bannerText(
	state: TerminalSessionState,
	t: TFunction,
	error?: string,
): string | undefined {
	if (state === "reattaching") return t("terminal.reattaching");
	if (state === "error") return t("terminal.error", { error: error ?? t("terminal.connectionFailed") });
	return undefined;
}

export function AttachedTerminal({
	session,
	theme,
	daemonReady,
	terminalTarget,
	fontSize,
	onChangeFontSize,
	isFullscreen,
	onToggleFullscreen,
	inputDisabled,
	booting,
	switchingLabel,
	focusRequested,
	onTerminalStateChange,
	inputRequest,
	onInputRequestResult,
	createMux,
	isVisible = true,
	onFatal,
	onTerminalReady,
}: TerminalPaneProps & {
	isVisible?: boolean;
	onFatal?: (message: string) => void;
	onTerminalReady?: (terminal: AttachableTerminal) => void;
}) {
	const { t } = useTranslation();
	const attachSession =
		terminalTarget?.kind === "shell"
			? undefined
			: session && terminalTarget?.kind === "reviewer"
			? { ...session, terminalHandleId: terminalTarget.handleId }
			: session;
	// One terminal instance per logical-terminal + handle generation. The shell
	// cache retains this component across route switches; a replacement handle
	// gets a new component rather than inheriting stale screen/input state.
	const [terminal, setTerminal] = useState<AttachableTerminal | null>(null);
	const [hasVisibleContent, setHasVisibleContent] = useState(false);
	const lastInputRequestIdRef = useRef<number | null>(null);
	const [initFailed, setInitFailed] = useState(false);
	const [isRestoring, setIsRestoring] = useState(false);
	const [restoreError, setRestoreError] = useState<string | undefined>();
	const [restoreUnavailable, setRestoreUnavailable] = useState(false);
	const queryClient = useQueryClient();
	const restoreSessionById = useRestoreSession();
	// A shell pane has no session, so it hands the hook its handle directly
	// instead of reading one off `attachSession`.
	const shellTerminalHandleId = terminalTarget?.kind === "shell" ? terminalTarget.handleId : undefined;
	const { attach, state, error, replaySettled, syncVisibleSize } = useTerminalSession(attachSession, {
		coverInitialReplay: terminalTarget?.kind !== "reviewer",
		createMux,
		daemonReady,
		exitNotice:
			terminalTarget?.kind === "reviewer"
				? "\r\n\x1b[2m[reviewer terminal finished]\x1b[0m"
				: undefined,
		inputDisabled,
		isVisible,
		shellTerminalHandleId,
	});
	useEffect(() => {
		onTerminalStateChange?.(state);
	}, [onTerminalStateChange, state]);
	useEffect(() => {
		if (!terminal || state !== "attached" || !inputRequest) return;
		if (lastInputRequestIdRef.current === inputRequest.id) return;
		const accepted = terminal.sendUserInput(inputRequest.data);
		if (accepted) lastInputRequestIdRef.current = inputRequest.id;
		onInputRequestResult?.(inputRequest.id, accepted);
	}, [inputRequest, onInputRequestResult, state, terminal]);
	// xterm's write callback means the replay has been parsed, not that the
	// browser has painted its final viewport. Keep the first-load cover mounted
	// through the same render/paint preparation used when activating a retained
	// terminal; otherwise large TUI replays can visibly repaint from their first
	// row immediately after the loading cover disappears.
	const [replayPaintPending, setReplayPaintPending] = useState(!replaySettled);
	useLayoutEffect(() => {
		if (!replaySettled) {
			setReplayPaintPending(true);
			return;
		}
		if (!replayPaintPending || !terminal) return;
		let current = true;
		void terminal.prepareForActivation().then(() => {
			if (current) setReplayPaintPending(false);
		});
		return () => {
			current = false;
		};
	}, [replayPaintPending, replaySettled, terminal]);
	const handleId = shellTerminalHandleId ?? attachSession?.terminalHandleId;
	const provider =
		terminalTarget?.kind === "reviewer"
			? terminalTarget.harness
			: terminalTarget?.kind === "shell"
				? undefined
				: session?.provider;
	const isSessionActive = session ? sessionIsActive(session) : false;
	// A standalone shell is never restorable: there is no session row to restore.
	const canRestoreSession =
		terminalTarget?.kind !== "reviewer" &&
		terminalTarget?.kind !== "shell" &&
		session !== undefined &&
		!isSessionActive;

	const handleReady = useCallback((handle: AttachableTerminal) => {
		setTerminal(handle);
	}, []);
	useLayoutEffect(() => {
		if (terminal) onTerminalReady?.(terminal);
	}, [onTerminalReady, terminal]);
	const handleInitError = useCallback((err: unknown) => {
		console.error("xterm failed to initialize", err);
		setInitFailed(true);
	}, []);
	useEffect(() => {
		if (initFailed) {
			onFatal?.("renderer initialization failed");
			onTerminalStateChange?.("error");
			return;
		}
	}, [initFailed, onFatal, onTerminalStateChange]);
	const handleLinkOpen = useSessionBrowserLink(session);
	const handleSessionLinkOpen = useSessionLinkNavigation(session?.hostId);
	const restoreSession = useCallback(async () => {
		if (!session?.id || !canRestoreSession || isRestoring) return;
		setIsRestoring(true);
		setRestoreError(undefined);
		try {
			const result = await restoreSessionById(session.id, session.hostId);
			if (result.status === "not_resumable") {
				setRestoreUnavailable(true);
				return;
			}
			if (result.status === "error") {
				setRestoreError(result.message);
			}
		} catch (err) {
			setRestoreError(err instanceof Error ? err.message : t("terminal.unableRestore"));
		} finally {
			setIsRestoring(false);
		}
	}, [canRestoreSession, isRestoring, restoreSessionById, session?.hostId, session?.id, t]);

	useEffect(() => {
		if (!terminal) return;
		let current = true;
		let detach: (() => void) | undefined;
		// A new xterm starts at its constructor default (80×24). Opening the PTY
		// before FitAddon has measured its real slot makes full-screen worker TUIs
		// redraw once at 80×24 and again at the actual grid. Settle that first fit
		// before attaching so the daemon receives only the authoritative size.
		void terminal.prepareForActivation().then(() => {
			if (!current) return;
			detach = attach(terminal);
		});
		return () => {
			current = false;
			detach?.();
		};
	}, [terminal, handleId, attach, attachSession?.id]);

	if (initFailed) {
		return (
			<div className="terminal-surface grid h-full place-items-center p-4 font-mono text-xs text-muted-foreground">
				{t("terminal.initFailed")}
			</div>
		);
	}

	const banner = bannerText(state, t, error);
	const showEmptyState = !handleId;
	// Cover xterm while the attachment buffers the initial replay, so the pane
	// appears already drawn at the tail instead of visibly scrolling down to it.
	// Deliberately NOT the empty state above: that renders a centered "Starting
	// session" card, and flashing it on every session switch would be worse than
	// the scroll it replaces.
	// Only while a replay is actually imminent. Gating on the state as well as
	// the gate keeps the cover from reappearing over a pane that is visibly
	// disconnected: an open timeout lifts it, the backoff reconnect would
	// otherwise pull it straight back down, and the "reattaching" banner already
	// explains that window better than a blank overlay does.
	const showReplayCover =
		Boolean(handleId) &&
		(!replaySettled || replayPaintPending) &&
		(state === "connecting" || state === "attached");
	// Also when the agent exited but its pane survived on a keep-alive: the mux
	// never reports "exited" there, so without this the strip — and the resume
	// action on it — stays hidden in exactly the state that needs it (#3875).
	const showEndedStatePreview =
		state === "exited" || canRestoreSession || (terminalTarget?.kind === "worker" && sessionAgentExited(session));
	// A terminal the move covered stays covered until the agent draws its first
	// frame, even after the move commits. ponytail: no timeout; an agent that
	// never draws exits, and the exited state ends the cover.
	const switchingHeldRef = useRef<string | undefined>(undefined);
	if (switchingLabel && !hasVisibleContent) switchingHeldRef.current = switchingLabel;
	if (hasVisibleContent || state === "exited" || state === "error") switchingHeldRef.current = undefined;
	const coverLabel = switchingLabel ?? switchingHeldRef.current;
	const switchingCover = Boolean(coverLabel) && !hasVisibleContent && state !== "exited" && state !== "error";
	const showEndedState = showEndedStatePreview && !booting && !coverLabel;

	return (
		<div className="terminal-surface flex h-full min-h-0 flex-col" data-testid="session-terminal">
			{showEndedState && (
				<TerminalEndedStrip
					canRestore={canRestoreSession}
					error={restoreError}
					isRestoring={isRestoring}
					onRestore={restoreSession}
					session={session}
					variant={
						terminalTarget?.kind === "reviewer" ? "reviewer" : terminalTarget?.kind === "shell" ? "shell" : "session"
					}
					reviewStatus={terminalTarget?.kind === "reviewer" ? terminalTarget.reviewStatus : undefined}
				/>
			)}
			{/* Keep a small gutter where terminal output starts, but let xterm use the
			    full top/right/bottom extent. The host fills the remaining
			    content box, so FitAddon still measures it correctly and the absolute
			    overlays (empty state, banner) keep covering the full padding box. */}
			<div className="relative min-h-0 flex-1 pl-2">
				<LazyChunkBoundary>
				<Suspense fallback={null}>
				<XtermTerminal
					ariaLabel={terminalTarget?.kind === "shell" ? t("terminal.shellAria") : t("terminal.sessionAria")}
					fontSize={fontSize}
					focusRequested={focusRequested}
					isFullscreen={isFullscreen}
					isVisible={isVisible}
					onChangeFontSize={onChangeFontSize}
					onError={handleInitError}
					onLinkOpen={handleLinkOpen}
					onSessionLinkOpen={handleSessionLinkOpen}
					onReady={handleReady}
					onVisibleContent={() => setHasVisibleContent(true)}
					onToggleFullscreen={onToggleFullscreen}
					onVisibleSize={syncVisibleSize}
					paneScrollsByKeyboard={providerScrollsByKeyboard(provider)}
					theme={theme}
				/>
				</Suspense>
				</LazyChunkBoundary>
				{(showEmptyState || booting || switchingCover) && (
					<div className="terminal-surface absolute inset-0 grid place-items-center font-mono text-control">
						{session ? (
							<TerminalBootStatus subject={session.kind === "orchestrator" ? "orchestrator" : "agent"} label={coverLabel} />
						) : (
							<div className="text-center">
								<div className="text-terminal">{PRODUCT_NAME}</div>
								<div className="mt-2 text-terminal-dim">{t("terminal.noSessionSelected")}</div>
							</div>
						)}
					</div>
				)}
				{showReplayCover && <ReplayCover />}
				{banner && !showEndedState && (
					<div className="absolute inset-x-3 top-2 rounded-md border border-border bg-surface/95 px-3 py-1.5 font-mono text-caption text-muted-foreground">
						{banner}
					</div>
				)}
			</div>
			{session && (
				<RestoreUnavailableDialog
					open={restoreUnavailable}
					session={session}
					onOpenChange={setRestoreUnavailable}
					onRecreated={async () => {
						await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(session.hostId) });
					}}
				/>
			)}
		</div>
	);
}
