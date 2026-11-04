import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { describe, expect, it } from "vitest";

const require = createRequire(import.meta.url);
const { setLaunchActionRelease } = require("./withReleaseScheme.js");

const scheme = `<LaunchAction
      buildConfiguration = "Debug"
      selectedDebuggerIdentifier = "Xcode.DebuggerFoundation.Debugger.LLDB">
   </LaunchAction>
   <TestAction
      buildConfiguration = "Debug">
   </TestAction>`;

describe("withReleaseScheme", () => {
	it("sets LaunchAction to Release and leaves other actions alone", () => {
		const out = setLaunchActionRelease(scheme);
		expect(out).toMatch(/<LaunchAction\s+buildConfiguration = "Release"/);
		expect(out).toContain('<TestAction\n      buildConfiguration = "Debug"');
	});
});

describe("app.json", () => {
	it("tracks the iOS buildNumber", () => {
		const app = JSON.parse(readFileSync(new URL("../app.json", import.meta.url), "utf8"));
		expect(Number(app.expo.ios.buildNumber)).toBeGreaterThanOrEqual(7);
	});
});
