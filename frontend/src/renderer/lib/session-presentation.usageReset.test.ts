import { describe, expect, it } from "vitest";
import { sessionActivityLabel } from "./session-presentation";

const t = ((key: string, vars?: { time?: string }) => `${key}|${vars?.time ?? ""}`) as never;

describe("session usage reset wait", () => {
	it("labels a paused session with the reset, not its activity", () => {
		const label = sessionActivityLabel({ activity: null, pausedUntil: "2026-11-02T06:20:00Z" }, t, "en-US");
		expect(label).toMatch(/^session\.waitingForUsageReset\|.*\d/);
	});
});
