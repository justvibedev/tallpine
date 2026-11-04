import type { TerminalConnectEnv } from "./terminal-connect-types";
import { defaultCreateMux } from "./terminal-session-constants";

export function connectSetup(env: TerminalConnectEnv) {
	const {
		optionsRef,
		runtime,
		teardownMux,
	} = env;
	const r = runtime.current;
	const { terminal, handle } = r;
	if (!terminal || !handle || r.detached) {
		return undefined;
	}
	// Flush the outgoing attachment BEFORE bumping the generation: past that
	// point its own guard rejects the flush and its buffered bytes are lost.
	r.flushReplay?.(true);
	const generation = r.generation + 1;
	r.generation = generation;
	r.inputReady = false;
	teardownMux();

	const baseMux = (optionsRef.current.createMux ?? defaultCreateMux)();
	const mux = baseMux;
	r.mux = mux;

	// Live output: one xterm.write in flight at a time. A burst arrives as many
	// WebSocket messages, and writing each separately costs a parse/render
	// cycle per message. Output arriving while xterm parses the previous write
	// is joined and written as one batch when that write completes, so batches
	// grow with load while an isolated chunk (a keystroke's echo) is written
	// immediately. The backlog lives here, bounded by the daemon's
	// back-pressure, rather than in xterm's own write queue.
	return {
		generation,
		handle,
		mux,
		r,
		terminal,
	};
}
