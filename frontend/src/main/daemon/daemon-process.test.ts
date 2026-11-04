import { describe, expect, it, vi } from "vitest";
import { killDaemon } from "./daemon-process";

describe("killDaemon", () => {
	it("does nothing without a pid", () => {
		const kill = vi.fn();
		const killPid = vi.fn();
		killDaemon({ pid: undefined, kill }, killPid);
		expect(killPid).not.toHaveBeenCalled();
		expect(kill).not.toHaveBeenCalled();
	});

	it("terminates the whole process group", () => {
		const kill = vi.fn();
		const killPid = vi.fn();
		killDaemon({ pid: 42, kill }, killPid);
		expect(killPid).toHaveBeenCalledWith(-42, "SIGTERM");
		expect(kill).not.toHaveBeenCalled();
	});

	it("falls back to the child when the group kill throws", () => {
		const kill = vi.fn();
		killDaemon({ pid: 42, kill }, () => {
			throw new Error("ESRCH");
		});
		expect(kill).toHaveBeenCalledWith("SIGTERM");
	});
});
