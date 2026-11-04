/** Pure helpers and DOM parking for the shell-owned terminal cache. */
import { terminalResetNonce } from "../../stores/terminal-reset-store";
import { terminalTargetBelongsToSession, type TerminalTarget } from "../../types/terminal";
import { type WorkspaceSession } from "../../types/workspace";
import type { TerminalPaneProps, TerminalCacheDescriptor, CachedTerminalEntry } from "./terminal-pane-types";

export function terminalTargetMatches(left?: TerminalTarget, right?: TerminalTarget): boolean {
	if (left === right) return true;
	if (!left || !right || left.kind !== right.kind) return false;
	if (left.kind === "worker" && right.kind === "worker") return true;
	if (left.kind === "reviewer" && right.kind === "reviewer") {
		return left.handleId === right.handleId && left.harness === right.harness;
	}
	if (left.kind === "shell" && right.kind === "shell") {
		return (
			left.handleId === right.handleId &&
			left.generation === right.generation &&
			left.title === right.title
		);
	}
	return false;
}

export function terminalPropsMatch(left: TerminalPaneProps, right: TerminalPaneProps): boolean {
	return (
		left.session === right.session &&
		left.theme === right.theme &&
		left.daemonReady === right.daemonReady &&
		left.fontSize === right.fontSize &&
		left.onChangeFontSize === right.onChangeFontSize &&
		left.isFullscreen === right.isFullscreen &&
		left.onToggleFullscreen === right.onToggleFullscreen &&
		left.inputDisabled === right.inputDisabled &&
		left.booting === right.booting &&
		left.switchingLabel === right.switchingLabel &&
		left.focusRequested === right.focusRequested &&
		left.onTerminalStateChange === right.onTerminalStateChange &&
		left.inputRequest === right.inputRequest &&
		left.onInputRequestResult === right.onInputRequestResult &&
		left.createMux === right.createMux &&
		terminalTargetMatches(left.terminalTarget, right.terminalTarget)
	);
}

export function cacheDescriptor(
	session: WorkspaceSession | undefined,
	terminalTarget: TerminalTarget | undefined,
	terminalGeneration?: string,
): TerminalCacheDescriptor | null {
	if (terminalTarget?.kind === "shell") {
		if (!terminalTargetBelongsToSession(terminalTarget, session?.id)) return null;
		const ownerKey = `shell:${session?.id ?? "standalone"}:${terminalTarget.handleId}`;
		return {
			cacheKey: `${ownerKey}|handle:${terminalTarget.handleId}|generation:${terminalTarget.generation}`,
			generation: terminalTarget.generation,
			handleId: terminalTarget.handleId,
			kind: "shell",
			ownerKey,
			sessionId: session?.id,
		};
	}

	// Reviewer terminals stream directly into a fresh mount and are intentionally
	// outside the retained worker/shell cache.
	if (terminalTarget?.kind === "reviewer") return null;
	const handleId = session?.terminalHandleId;
	if (!session?.id || !handleId) return null;
	const ownerKey = `session:${session.id}:worker`;
	const generation = terminalGeneration ?? session.terminalGeneration ?? "";
	// The relaunch nonce discriminates a relaunched terminal behind the same
	// handle from the live one; `generation` stays the raw value the workspace
	// reconcile loop compares against session.terminalGeneration.
	const nonce = terminalResetNonce(session.id);
	return {
		cacheKey: `${ownerKey}|handle:${handleId}|reset:${nonce}`,
		generation,
		handleId,
		kind: "worker",
		ownerKey,
		sessionId: session.id,
	};
}

export function blurTerminal(container: HTMLElement): void {
	const active = document.activeElement;
	if (active instanceof HTMLElement && container.contains(active)) {
		active.blur();
	}
}

export function setTerminalPhase(
	entry: CachedTerminalEntry,
	phase: CachedTerminalEntry["activationPhase"],
): void {
	entry.activationPhase = phase;
	entry.container.dataset.terminalActivationPhase = phase;
	const interactive = phase === "visible";
	entry.container.inert = !interactive;
	if (interactive) {
		entry.container.removeAttribute("aria-hidden");
		entry.container.style.pointerEvents = "";
	} else {
		entry.container.setAttribute("aria-hidden", "true");
		entry.container.style.pointerEvents = "none";
	}
	if (interactive) {
		entry.container.style.visibility = "";
		entry.container.style.contentVisibility = "";
	} else {
		entry.container.style.visibility = "hidden";
		// `visibility: hidden` still lays out the complete xterm subtree. Parked
		// terminals retain their buffer and keep accepting output, but are never
		// fitted while hidden, so Chromium can skip their expensive DOM layout
		// until the container returns to the active pane.
		entry.container.style.contentVisibility = "hidden";
	}
}

export function parkTerminal(entry: CachedTerminalEntry, parking: HTMLDivElement): void {
	entry.activationId += 1;
	blurTerminal(entry.container);
	setTerminalPhase(entry, "parked");
	parking.appendChild(entry.container);
}

export function showTerminal(entry: CachedTerminalEntry, slot: HTMLDivElement): void {
	entry.activationId += 1;
	// Do not hide a retained terminal while it crosses xterm's two paint-frame
	// preparation cycle: that made every return to a tab flash blank.
	entry.container.style.width = "100%";
	entry.container.style.height = "100%";
	slot.appendChild(entry.container);
	setTerminalPhase(entry, "visible");
}
