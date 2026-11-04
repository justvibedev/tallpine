import { describe, expect, it } from "vitest";
import { parseTimedScriptCommand, timedScriptCommand } from "./script-steps";

describe("timed script steps", () => {
	it("writes and reads the tallpine directive", () => {
		const stored = timedScriptCommand(45, "npm ci");
		expect(stored).toBe("# tallpine-timeout: 45m\nnpm ci");
		expect(parseTimedScriptCommand(stored)).toEqual({ minutes: 45, command: "npm ci" });
	});

	it("still reads the pre-rename ao directive", () => {
		expect(parseTimedScriptCommand("# ao-timeout: 30m\nnpm ci")).toEqual({ minutes: 30, command: "npm ci" });
	});

	it("treats a step without a directive as untimed", () => {
		expect(parseTimedScriptCommand("npm ci")).toEqual({ command: "npm ci" });
	});
});
