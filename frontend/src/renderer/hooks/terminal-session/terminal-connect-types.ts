import type { useTerminalSessionBase } from "./useTerminalSessionBase";

/** Hook-scope refs, state and callbacks the connect installers read. */
export type TerminalConnectEnv = ReturnType<typeof useTerminalSessionBase>;

/** Mutable values the connect installers share (one object per connect call). */
export type TerminalConnectState = {
	pendingReplayWrites: number;
	postReplayWriteActive: boolean;
	tailHoldsHistory: boolean;
};
