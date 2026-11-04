import { afterEach, describe, expect, it, vi } from "vitest";
import { DEFAULT_UPDATE_SETTINGS } from "./update-settings";

// Without a build-time team id (unsigned/dev build) updates must stay off, even
// when the running app also reports an empty team.
describe("planUpdates without an injected team id", () => {
	afterEach(() => vi.unstubAllGlobals());
	for (const stub of ["empty", "undefined"] as const) {
		it(`is unsigned when the team define is ${stub}`, async () => {
			vi.resetModules();
			if (stub === "empty") vi.stubGlobal("__TALLPINE_TEAM_ID__", "");
			const { planUpdates } = await import("./update-eligibility");
			const env = { packaged: true, platform: "darwin", version: "1.4.0", buildDirty: "false", teamId: "" } as const;
			expect(planUpdates(env as never, { ...DEFAULT_UPDATE_SETTINGS, enabled: true }, {})).toEqual({ active: false, reason: "unsigned" });
		});
	}
});
