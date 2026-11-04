import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { startPlanPolling } from "./planPolling";

function fakeAppState(initial: string) {
	let listener: (s: string) => void = () => {};
	const remove = vi.fn();
	return {
		currentState: initial,
		addEventListener: (_: "change", l: (s: string) => void) => { listener = l; return { remove }; },
		emit: (s: string) => listener(s),
		remove,
	};
}

describe("startPlanPolling", () => {
	beforeEach(() => vi.useFakeTimers());
	afterEach(() => vi.useRealTimers());

	it("polls while active, stops in the background, refreshes once on return", () => {
		const app = fakeAppState("active");
		const refresh = vi.fn();
		const stop = startPlanPolling(refresh, app, 1000);
		expect(refresh).toHaveBeenCalledTimes(1);
		vi.advanceTimersByTime(2000);
		expect(refresh).toHaveBeenCalledTimes(3);
		app.emit("background");
		vi.advanceTimersByTime(5000);
		expect(refresh).toHaveBeenCalledTimes(3);
		app.emit("active");
		expect(refresh).toHaveBeenCalledTimes(4);
		vi.advanceTimersByTime(1000);
		expect(refresh).toHaveBeenCalledTimes(5);
		stop();
		vi.advanceTimersByTime(5000);
		expect(refresh).toHaveBeenCalledTimes(5);
		expect(app.remove).toHaveBeenCalled();
	});

	it("does not poll when started in the background", () => {
		const app = fakeAppState("background");
		const refresh = vi.fn();
		startPlanPolling(refresh, app, 1000);
		vi.advanceTimersByTime(3000);
		expect(refresh).not.toHaveBeenCalled();
	});
});
