import { useCallback } from "react";
import type { TerminalConnectEnv, TerminalConnectState } from "./terminal-connect-types";
import { connectSetup } from "./connect-setup";
import { connectLive } from "./connect-live";
import { connectReplay } from "./connect-replay";
import { connectMux } from "./connect-mux";
import { connectInput } from "./connect-input";

/** Builds the connect callback that (re)binds the terminal to a fresh mux attachment. */
export function useTerminalConnect(env: TerminalConnectEnv) {
	const {
		clearOpenTimer,
		clearReplayTimers,
		connectRef,
		invalidateWorkspaces,
		isCurrentAttachment,
		scheduleReattach,
		teardownMux,
		transition,
	} = env;
	const connect = useCallback(() => {
		const st = {} as TerminalConnectState;
		const setupOut = connectSetup(env);
		if (!setupOut) return;
		const liveOut = connectLive(env, setupOut);
		const replayOut = connectReplay(env, st, setupOut);
		connectMux(env, st, setupOut, liveOut, replayOut);
		connectInput(env, setupOut, replayOut);
	}, [
		clearOpenTimer,
		clearReplayTimers,
		invalidateWorkspaces,
		isCurrentAttachment,
		scheduleReattach,
		teardownMux,
		transition,
	]);
	connectRef.current = connect;
	return connect;
}
