import type { TerminalConnectEnv } from "./terminal-connect-types";
import { connectSetup } from "./connect-setup";

export function connectLive(env: TerminalConnectEnv, setupOut: NonNullable<ReturnType<typeof connectSetup>>) {
	const {
		isCurrentAttachment,
	} = env;
	const {
		generation,
		handle,
		mux,
		terminal,
	} = setupOut;
	let liveChunks: Uint8Array[] = [];
	let liveBytes = 0;
	let liveWriteInFlight = false;
	const takeLiveBatch = (): Uint8Array | null => {
		if (liveChunks.length === 0) return null;
		let batch = liveChunks[0]!;
		if (liveChunks.length > 1) {
			batch = new Uint8Array(liveBytes);
			let offset = 0;
			for (const chunk of liveChunks) {
				batch.set(chunk, offset);
				offset += chunk.length;
			}
		}
		liveChunks = [];
		liveBytes = 0;
		return batch;
	};
	const writeLiveBatch = () => {
		const batch = takeLiveBatch();
		if (!batch) return;
		liveWriteInFlight = true;
		terminal.write(batch, () => {
			liveWriteInFlight = false;
			if (isCurrentAttachment(generation, handle, mux)) writeLiveBatch();
		});
	};
	const writeLiveOutput = (bytes: Uint8Array) => {
		liveChunks.push(bytes);
		liveBytes += bytes.length;
		if (!liveWriteInFlight) writeLiveBatch();
	};
	// Hand any held output to xterm now, in order, ahead of a teardown or a
	// write that must follow it.
	const drainLiveOutput = () => {
		const batch = takeLiveBatch();
		if (batch) terminal.write(batch);
	};

	return {
		drainLiveOutput,
		writeLiveOutput,
	};
}
