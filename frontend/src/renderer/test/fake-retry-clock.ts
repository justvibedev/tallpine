import { onTestFinished, vi } from "vitest";

/**
 * Runs production retry/poll delays (react-query backoff, polling intervals) on
 * a fake clock so a test does not wait them out in real time. Call it before the
 * code under test schedules its timer; real timers return when the test ends.
 */
export function fakeRetryClock() {
	vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval"] });
	onTestFinished(() => { vi.useRealTimers(); });
}

/** vi.waitFor that advances the fake clock by `stepMs` (one retry delay) per check. */
export function waitForRetry(assertion: () => void, stepMs = 1_000, timeout = 3_000) {
	return vi.waitFor(async () => {
		await vi.advanceTimersByTimeAsync(stepMs);
		assertion();
	}, { timeout });
}

/** Lets a pending call that fails and retries finish on the fake clock. */
export async function settleRetry(pending: Promise<unknown>, stepMs = 1_000) {
	await vi.advanceTimersByTimeAsync(stepMs);
	await pending;
}
