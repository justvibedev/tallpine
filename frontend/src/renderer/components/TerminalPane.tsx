import { useTerminalResetStore } from "../stores/terminal-reset-store";
import { useContext } from "react";
import { useTranslation } from "react-i18next";
import { terminalTargetBelongsToSession, type TerminalTarget } from "../types/terminal";
import { TerminalCacheContext, type TerminalPaneProps } from "./terminal/terminal-pane-types";
import { reviewerPreviewLines, workerPreviewLines } from "./terminal/terminal-preview-lines";
import { cacheDescriptor } from "./terminal/terminal-cache-helpers";
import { CachedTerminalSlot } from "./terminal/CachedTerminalSlot";
import { AttachedTerminal } from "./terminal/AttachedTerminal";

export function TerminalPane({
	session,
	terminalGeneration,
	theme,
	daemonReady,
	terminalTarget: requestedTerminalTarget,
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
}: TerminalPaneProps) {
	const { t } = useTranslation();
	const terminalTarget =
		requestedTerminalTarget &&
		terminalTargetBelongsToSession(requestedTerminalTarget, session?.id)
			? requestedTerminalTarget
			: ({ kind: "worker" } satisfies TerminalTarget);
	const isOptimisticShell =
		terminalTarget.kind === "shell" && terminalTarget.handleId.startsWith("pending-shell:");
	const cache = useContext(TerminalCacheContext);
	// Subscribe to this session's relaunch nonce so a relaunch re-renders the pane
	// and recomputes the descriptor below.
	useTerminalResetStore((state) => (session?.id ? (state.nonces[session.id] ?? 0) : 0));
	const terminalKey =
		terminalTarget?.kind === "reviewer" || terminalTarget?.kind === "shell"
			? terminalTarget.handleId
			: `${session?.terminalHandleId ?? "empty"}:${session?.terminalGeneration ?? ""}`;

	if (!window.tallpine) {
		// A standalone shell has no agent and no branch, so it previews as a plain
		// prompt rather than borrowing the session's agent transcript.
		if (terminalTarget?.kind === "shell") {
			return (
				<pre
					className="h-full overflow-auto bg-terminal p-4 font-mono leading-relaxed text-terminal"
					style={{ fontSize }}
				>
					<span className="text-terminal-dim">{terminalTarget.title}</span> $ {"\n"}
					<span className="text-terminal-dim">
						{"(standalone shell — a live PTY here in the desktop app)"}
						{"\n"}
					</span>
				</pre>
			);
		}
		const provider = terminalTarget?.kind === "reviewer" ? terminalTarget.harness : (session?.provider ?? "claude");
		const lines =
			terminalTarget?.kind === "reviewer" ? reviewerPreviewLines(session) : workerPreviewLines(session, provider);
		return (
			<pre
				className="h-full overflow-auto bg-terminal p-4 font-mono leading-relaxed text-terminal-foreground"
				data-testid="session-terminal"
				style={{ fontSize }}
			>
				<span className="text-terminal-dim">~/{session?.workspaceName ?? "reverbcode"}</span>{" "}
				{session?.branch ? <span className="text-accent">{session.branch}</span> : null}
				{session?.branch ? " " : ""}$ {provider}
				{"\n"}
				{lines.map((line, index) => (
					<span
						key={`${line}:${index}`}
						className={
							line.startsWith("PASS") || line.startsWith("DONE")
								? "text-success"
								: line.startsWith("WARN") || line.startsWith("TODO")
									? "text-warning"
									: line.startsWith("$") || line.startsWith("▲")
										? "text-accent"
										: "text-terminal-foreground"
						}
					>
						{line}
						{"\n"}
					</span>
				))}
			</pre>
		);
	}
	// The tab is intentionally selected before the daemon allocates its handle.
	// Keep that short bridge visually indistinguishable from an empty terminal,
	// rather than surfacing a separate loading state or attaching xterm to a
	// handle that cannot exist yet.
	if (isOptimisticShell) {
		return (
			<div
				aria-label={t("terminal.shellAria")}
				className="terminal-surface h-full"
				data-testid="optimistic-terminal"
			/>
		);
	}

	const props = {
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
	};
	const descriptor = cacheDescriptor(session, terminalTarget, terminalGeneration);
	// The retained cache validates shells against the local daemon's shell list.
	// A caller-owned transport may belong to another host.
	if (cache && descriptor && !createMux) {
		return <CachedTerminalSlot descriptor={descriptor} props={props} />;
	}

	return (
		<AttachedTerminal
			key={terminalKey}
			session={session}
			theme={theme}
			daemonReady={daemonReady}
			fontSize={fontSize}
			isFullscreen={isFullscreen}
			inputDisabled={inputDisabled}
			booting={booting}
			switchingLabel={switchingLabel}
			onChangeFontSize={onChangeFontSize}
			onToggleFullscreen={onToggleFullscreen}
			focusRequested={focusRequested}
			onTerminalStateChange={onTerminalStateChange}
			inputRequest={inputRequest}
			onInputRequestResult={onInputRequestResult}
			createMux={createMux}
			terminalTarget={terminalTarget}
		/>
	);
}

