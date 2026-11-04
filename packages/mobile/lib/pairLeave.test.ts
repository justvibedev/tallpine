import { describe, expect, it, vi } from "vitest";
import { waitForSheetToSettle, withTimeout } from "./pairLeave";

describe("waitForSheetToSettle", () => {
	it("waits for the scanner to regain focus, then for the sheet's animation", async () => {
		// The name sheet is still in front for three polls after "Save".
		let polls = 0;
		const events: string[] = [];
		const isFocused = () => polls >= 3;
		const sleep = vi.fn(async (ms: number) => { polls += 1; events.push(`sleep ${ms}`); });

		await waitForSheetToSettle({ isFocused, sleep }, { pollMs: 50, settleMs: 450 });

		expect(events).toEqual(["sleep 50", "sleep 50", "sleep 50", "sleep 450"]);
	});

	it("goes anyway once focus never returns", async () => {
		const sleep = vi.fn(async () => {});
		await waitForSheetToSettle({ isFocused: () => false, sleep }, { focusTimeoutMs: 200, pollMs: 50, settleMs: 10 });
		expect(sleep).toHaveBeenCalledTimes(5);
	});
});

describe("withTimeout", () => {
	it("returns the fallback when the work outlives the limit", async () => {
		vi.useFakeTimers();
		const pending = withTimeout(new Promise<string>(() => {}), 8000, "late");
		vi.advanceTimersByTime(8000);
		await expect(pending).resolves.toBe("late");
		vi.useRealTimers();
	});

	it("returns the work's value when it finishes first", async () => {
		await expect(withTimeout(Promise.resolve("ok"), 8000, "late")).resolves.toBe("ok");
	});
});
