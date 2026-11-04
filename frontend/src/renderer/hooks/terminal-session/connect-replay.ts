import { consumeFreshTerminalHandle } from "../../lib/fresh-terminal-handles";
import type { TerminalWriteSource } from "../useTerminalSession";
import type { TerminalConnectEnv, TerminalConnectState } from "./terminal-connect-types";
import { connectSetup } from "./connect-setup";
import { REPLAY_TAIL_QUIET_MS, REPLAY_TAIL_CAP_MS, REPLAY_WRITE_BATCH_BYTES } from "./terminal-session-constants";

export function connectReplay(env: TerminalConnectEnv, st: TerminalConnectState, setupOut: NonNullable<ReturnType<typeof connectSetup>>) {
	const {
		clearReplayTimers,
		isCurrentAttachment,
		setReplaySettled,
	} = env;
	const {
		generation,
		handle,
		mux,
		r,
		terminal,
	} = setupOut;
	st.pendingReplayWrites = 0;
	let replayRevealDeadlineReached = false;
	const postReplayWriteQueue: Uint8Array[] = [];
	st.postReplayWriteActive = false;
	let replayBatchBytes: Uint8Array | null = null;
	let replayBatchOffset = 0;
	let replayBatchTimer: ReturnType<typeof setTimeout> | null = null;
	let replayBatchDone: (() => void) | null = null;
	let replayWritesPreserved = false;
	// Only a newly created handle can have live initial output. Component
	// mounts, including the first mount after app startup, can replay history.
	const initialWriteSource: TerminalWriteSource = consumeFreshTerminalHandle(handle) ? "live" : "replay";

	// Reveal only after xterm has parsed the coalesced replay and any late tail
	// frames have gone quiet. The tail itself streams straight into xterm behind
	// the cover, avoiding an unbounded duplicate replay buffer.
	const revealReplayTail = () => {
		if (!r.replayTailPending || !isCurrentAttachment(generation, handle, mux)) return;
		if (r.replayTailQuietTimer) {
			clearTimeout(r.replayTailQuietTimer);
			r.replayTailQuietTimer = null;
		}
		if (r.replayTailCapTimer) {
			clearTimeout(r.replayTailCapTimer);
			r.replayTailCapTimer = null;
		}
		if (st.pendingReplayWrites > 0) {
			replayRevealDeadlineReached = true;
			return;
		}
		r.replayTailPending = false;
		replayRevealDeadlineReached = false;
		terminal.showLatestOutput();
		setReplaySettled(true);
	};
	const scheduleReplayTailReveal = () => {
		if (!r.replayTailPending || !isCurrentAttachment(generation, handle, mux)) return;
		if (st.pendingReplayWrites > 0) return;
		if (replayRevealDeadlineReached) {
			revealReplayTail();
			return;
		}
		if (r.replayTailQuietTimer) clearTimeout(r.replayTailQuietTimer);
		r.replayTailQuietTimer = setTimeout(revealReplayTail, REPLAY_TAIL_QUIET_MS);
	};
	const holdReplayTail = () => {
		r.replayTailPending = true;
		if (!r.replayTailCapTimer) {
			r.replayTailCapTimer = setTimeout(revealReplayTail, REPLAY_TAIL_CAP_MS);
		}
	};
	// The mux does not distinguish historical bytes from fresh PTY output, so
	// the handle's creation state classifies the entire covered burst.
	const writeReplayBatches = (bytes: Uint8Array, done: () => void) => {
		replayBatchBytes = bytes;
		replayBatchOffset = 0;
		replayBatchDone = done;
		const writeNext = () => {
			replayBatchTimer = null;
			if (replayWritesPreserved) return;
			if (!isCurrentAttachment(generation, handle, mux)) return;
			const current = replayBatchBytes;
			if (!current) return;
			const end = Math.min(current.length, replayBatchOffset + REPLAY_WRITE_BATCH_BYTES);
			const batch = current.subarray(replayBatchOffset, end);
			replayBatchOffset = end;
			terminal.write(batch, () => {
				if (replayWritesPreserved) return;
				if (!isCurrentAttachment(generation, handle, mux)) return;
				if (replayBatchOffset >= current.length) {
					const finished = replayBatchDone;
					replayBatchBytes = null;
					replayBatchOffset = 0;
					replayBatchDone = null;
					finished?.();
					return;
				}
				replayBatchTimer = setTimeout(writeNext, 0);
			}, initialWriteSource);
		};
		writeNext();
	};
	const preservePendingReplayWrites = () => {
		if (replayWritesPreserved) return;
		replayWritesPreserved = true;
		if (replayBatchTimer !== null) {
			clearTimeout(replayBatchTimer);
			replayBatchTimer = null;
		}
		if (replayBatchBytes && replayBatchOffset < replayBatchBytes.length) {
			// The current batch is already in xterm's queue. Queue the remainder in
			// one call before dispose so it cannot be overtaken or discarded.
			terminal.write(replayBatchBytes.subarray(replayBatchOffset), undefined, initialWriteSource);
		}
		replayBatchBytes = null;
		replayBatchOffset = 0;
		replayBatchDone = null;
		for (const bytes of postReplayWriteQueue) terminal.write(bytes, undefined, initialWriteSource);
		postReplayWriteQueue.length = 0;
		st.postReplayWriteActive = false;
		st.pendingReplayWrites = 0;
		r.replayTailPending = false;
		terminal.showLatestOutput();
		setReplaySettled(true);
	};
	const settleAfterReplayWrites = () => {
		if (st.pendingReplayWrites > 0 || st.postReplayWriteActive || postReplayWriteQueue.length > 0) return;
		if (r.replayTailPending) {
			scheduleReplayTailReveal();
			return;
		}
		terminal.showLatestOutput();
		setReplaySettled(true);
	};
	// Frames after the replay went quiet are the agent's live output, so a colour
	// query in them is answered. Only a replay cut short by its size cap leaves
	// history in the tail.
	st.tailHoldsHistory = false;
	const drainPostReplayWrites = () => {
		if (!isCurrentAttachment(generation, handle, mux)) return;
		if (st.pendingReplayWrites > 0 || st.postReplayWriteActive) return;
		const bytes = postReplayWriteQueue.shift();
		if (!bytes) {
			settleAfterReplayWrites();
			return;
		}
		st.postReplayWriteActive = true;
		st.pendingReplayWrites += 1;
		terminal.write(bytes, () => {
			if (replayWritesPreserved) return;
			if (!isCurrentAttachment(generation, handle, mux)) return;
			st.postReplayWriteActive = false;
			st.pendingReplayWrites = Math.max(0, st.pendingReplayWrites - 1);
			drainPostReplayWrites();
		}, st.tailHoldsHistory ? initialWriteSource : "live");
	};

	// End the buffered part of the initial replay: concatenate what arrived so
	// far and feed it through bounded writes. Bytes arriving during those yields
	// queue behind the replay so ANSI/VT commands remain in exact wire order.
	// Normal quiet/cap flushes keep the cover for the tail. Explicit input settles
	// after the ordered queue drains; teardown synchronously submits any remainder
	// to xterm before releasing the attachment.
	//
	// Safe to call from anywhere — a second call is a no-op, and a call from
	// a superseded attachment is dropped.
	const flushReplay = (holdTail = false, preserveBeforeTeardown = false) => {
		if (!r.replayBuffering) {
			if (preserveBeforeTeardown) preservePendingReplayWrites();
			return;
		}
		if (!isCurrentAttachment(generation, handle, mux)) return;
		r.replayBuffering = false;
		clearReplayTimers();
		if (holdTail) holdReplayTail();

		const chunks = r.replayChunks;
		r.replayChunks = [];
		r.replayBytes = 0;

		if (chunks.length === 0) {
			// Nothing buffered, so there is nothing to reveal at a settled
			// position — just make sure the cover is off. Reaching here means an
			// exit, error, dead socket or the cap; a pane that is merely slow to
			// draw was already uncovered by REPLAY_FIRST_BYTE_MS without ending
			// the gate.
			if (holdTail) scheduleReplayTailReveal();
			else setReplaySettled(true);
			return;
		}

		let total = 0;
		for (const chunk of chunks) total += chunk.length;
		const replay = new Uint8Array(total);
		let offset = 0;
		for (const chunk of chunks) {
			replay.set(chunk, offset);
			offset += chunk.length;
		}
		if (preserveBeforeTeardown) {
			terminal.write(replay, undefined, initialWriteSource);
			preservePendingReplayWrites();
			return;
		}
		st.pendingReplayWrites += 1;
		writeReplayBatches(replay, () => {
			if (!isCurrentAttachment(generation, handle, mux)) return;
			st.pendingReplayWrites = Math.max(0, st.pendingReplayWrites - 1);
			drainPostReplayWrites();
		});
	};
	r.flushReplay = (preserveBeforeTeardown = false) =>
		flushReplay(false, preserveBeforeTeardown);

	return {
		drainPostReplayWrites,
		flushReplay,
		initialWriteSource,
		postReplayWriteQueue,
		revealReplayTail,
	};
}
