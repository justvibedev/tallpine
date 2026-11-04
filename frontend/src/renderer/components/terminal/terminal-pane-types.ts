/** Props and cache descriptor types shared by TerminalPane and the terminal cache. */
import { createContext } from "react";
import { type TerminalTarget } from "../../types/terminal";
import { type WorkspaceSession } from "../../types/workspace";
import type { Theme } from "../../stores/ui-store";
import { type AttachableTerminal, type TerminalSessionState } from "../../hooks/useTerminalSession";
import { type TerminalMux } from "../../lib/terminal-mux";

export type TerminalPaneProps = {
	session?: WorkspaceSession;
	terminalGeneration?: string;
	theme: Theme;
	daemonReady: boolean;
	terminalTarget?: TerminalTarget;
	fontSize: number;
	/** Resize this terminal without changing application zoom. */
	onChangeFontSize?: (delta: number) => void;
	isFullscreen?: boolean;
	/** Enter or exit fullscreen for the terminal pane that owns this xterm. */
	onToggleFullscreen?: () => void | Promise<void>;
	/** Refuse agent PTY input while a controller transition owns the source. */
	inputDisabled?: boolean;
	/** The agent terminal is coming up after an interface switch; show the boot spinner instead of ended/empty chrome. */
	booting?: boolean;
	/** An account move is restarting this agent in a new terminal. The label covers
	 * the pane while nothing is drawn there, instead of boot text and an empty
	 * terminal; the old screen stays until its terminal is gone. */
	switchingLabel?: string;
	/** Focus the terminal when an in-flight controller asks for human input. */
	focusRequested?: boolean;
	/** Observe attachment state without taking ownership of the terminal lifecycle. */
	onTerminalStateChange?: (state: TerminalSessionState) => void;
	/** One-shot input initiated by an explicit UI action. */
	inputRequest?: { id: number; data: string };
	/** Reports whether the active attachment accepted a one-shot input request. */
	onInputRequestResult?: (id: number, accepted: boolean) => void;
	/** Provider-owned shared transport lease factory. */
	createMux?: () => TerminalMux;
};

export type TerminalCacheDescriptor = {
	cacheKey: string;
	generation?: string;
	handleId: string;
	kind: "shell" | "worker";
	ownerKey: string;
	sessionId?: string;
};

export type CachedTerminalEntry = TerminalCacheDescriptor & {
	activationId: number;
	activationPhase: "parked" | "visible";
	container: HTMLDivElement;
	discardOnDeactivate?: boolean;
	props: TerminalPaneProps;
	terminal?: AttachableTerminal;
};

// isUnlisted reports whether the last workspace snapshot (listed; null before
// one arrived) does not list an entry's session. Such an entry is kept only
// while it is on screen (see the snapshot reconcile).
export function isUnlisted(entry: CachedTerminalEntry, listed: Set<string> | null): boolean {
	return Boolean(entry.sessionId) && listed !== null && !listed.has(entry.sessionId ?? "");
}

export type ActiveTerminalEntry = {
	key: string;
	slot: HTMLDivElement;
};

export type TerminalCacheController = {
	activate: (descriptor: TerminalCacheDescriptor, props: TerminalPaneProps, slot: HTMLDivElement) => void;
	deactivate: (cacheKey: string, slot: HTMLDivElement) => void;
	update: (cacheKey: string, props: TerminalPaneProps) => void;
	/** The stable transport factory for a pane: the local daemon pool. */
	resolveCreateMux: (session?: WorkspaceSession, terminalTarget?: TerminalTarget) => () => TerminalMux;
};

export const TerminalCacheContext = createContext<TerminalCacheController | null>(null);
