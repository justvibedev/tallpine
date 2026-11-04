import { describe, expect, it } from "vitest";
import { describeDesktopUpdate } from "./desktopUpdate";

const base = { installedCommit: "1111111aaaa", candidate: "2222222bbbb", updateAvailable: true, running: false };

describe("describeDesktopUpdate", () => {
	it("offers a CI-passed update", () => {
		expect(describeDesktopUpdate(base, false)).toEqual({ label: "Update 2222222 (CI passed)", canStart: true, note: undefined });
	});

	it("blocks a second start while one runs or is being sent", () => {
		expect(describeDesktopUpdate({ ...base, running: true }, false).canStart).toBe(false);
		expect(describeDesktopUpdate(base, true).canStart).toBe(false);
	});

	it("cannot start when up to date or before the status loads", () => {
		expect(describeDesktopUpdate({ ...base, updateAvailable: false }, false)).toMatchObject({ label: "Up to date", canStart: false });
		expect(describeDesktopUpdate(null, false).canStart).toBe(false);
	});

	it("explains a rolled back last update", () => {
		const row = describeDesktopUpdate({ ...base, updateAvailable: false, last: { state: "rolled_back", commit: "3333333c", message: "unhealthy" } }, false);
		expect(row.note).toBe("Last update to 3333333 was rolled back: unhealthy");
	});
});
