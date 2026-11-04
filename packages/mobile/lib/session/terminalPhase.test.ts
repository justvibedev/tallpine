import { describe, expect, it } from "vitest";
import { TERMINAL_ENHANCE_JS } from "./terminalEnhanceScript";
import { statusColorFor, terminalInterfacePhaseLabel } from "./terminalPhase";

describe("terminalInterfacePhaseLabel", () => {
	it("maps every transition phase and defaults to the resume check", () => {
		expect(terminalInterfacePhaseLabel("draining")).toMatch(/Waiting for the current terminal turn/);
		expect(terminalInterfacePhaseLabel("source_stopping")).toMatch(/Stopping the terminal controller/);
		expect(terminalInterfacePhaseLabel("source_stopped")).toMatch(/controller stopped/);
		expect(terminalInterfacePhaseLabel("target_starting")).toMatch(/Resuming the same native conversation/);
		expect(terminalInterfacePhaseLabel("activating")).toBe("Opening the Chat interface.");
		expect(terminalInterfacePhaseLabel()).toMatch(/Checking that Chat can resume/);
		expect(terminalInterfacePhaseLabel("unknown")).toMatch(/Checking that Chat can resume/);
	});
});

describe("statusColorFor", () => {
	it("colours each mux status from the theme", () => {
		const t = { amber: "a", green: "g", textTertiary: "t", red: "r" } as never;
		expect(statusColorFor(t)).toEqual({ connecting: "a", open: "g", closed: "t", error: "r" });
	});
});

describe("TERMINAL_ENHANCE_JS", () => {
	it("is the injected script: scroll hardening, IS_ANDROID branch, ends truthy", () => {
		expect(TERMINAL_ENHANCE_JS).toContain("harden()");
		expect(TERMINAL_ENHANCE_JS).toContain("IS_ANDROID");
		expect(TERMINAL_ENHANCE_JS.trim().endsWith("true;")).toBe(true);
	});
});
