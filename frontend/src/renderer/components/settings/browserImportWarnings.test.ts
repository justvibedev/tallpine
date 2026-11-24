import { describe, expect, it } from "vitest";
import { browserImportWarningText } from "./browserImportWarnings";

describe("browserImportWarningText", () => {
	it("names the device-bound sites and tells the person to sign in directly", () => {
		const text = browserImportWarningText({ code: "device-bound-sessions", count: 2, sites: ["claude.ai", "google.com"] });

		expect(text).toContain("claude.ai, google.com");
		expect(text).toMatch(/sign in to these sites directly/i);
	});

	it("keeps count-based warnings unchanged", () => {
		expect(browserImportWarningText({ code: "expired-cookies-skipped", count: 3 })).toContain("3");
	});
});
