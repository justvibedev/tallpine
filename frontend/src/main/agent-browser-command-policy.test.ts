import { describe, expect, it } from "vitest";
import { nativeArgumentsForAction } from "./agent-browser-runtime/native-actions";
import { validateAgentBrowserArguments } from "./agent-browser-runtime/validate";

describe("agent-browser command policy", () => {
	it("allows the focused semantic workflow", () => {
		expect(() => validateAgentBrowserArguments(["snapshot", "-i", "--json"])).not.toThrow();
		expect(() => validateAgentBrowserArguments(["find", "role", "button", "click", "--name", "Save"])).not.toThrow();
		expect(() => validateAgentBrowserArguments(["wait", "--text", "Saved"])).not.toThrow();
		expect(() => validateAgentBrowserArguments(["open", "http://localhost:5173"])).not.toThrow();
	});

	it("blocks browser ownership, persistence, arbitrary evaluation, and unsafe navigation", () => {
		for (const args of [
			["connect", "9222"],
			["eval", "document.cookie"],
			["snapshot", "--cdp", "9222"],
			["snapshot", "--profile", "Default"],
			["get", "cdp-url"],
			["open", "file:///tmp/secret"],
			["network", "route", "*", "--abort"],
		]) {
			expect(() => validateAgentBrowserArguments(args)).toThrow();
		}
	});
});

describe("Tallpine action translation", () => {
	it("maps the public snapshot and ref contract to native arguments", () => {
		expect(nativeArgumentsForAction("snapshot", { interactive: true })).toEqual([
			"snapshot",
			"--interactive",
			"--compact",
		]);
		expect(nativeArgumentsForAction("snapshot", { delta: true })).toEqual(["snapshot", "--compact", "--delta"]);
		expect(nativeArgumentsForAction("snapshot", { interactive: true, delta: true, full: true })).toEqual([
			"snapshot",
			"--interactive",
			"--compact",
			"--delta",
			"--full",
		]);
		expect(nativeArgumentsForAction("snapshot", { full: true })).toEqual(["snapshot", "--compact"]);
		expect(nativeArgumentsForAction("click", { ref: "e2" })).toEqual(["click", "@e2"]);
		expect(nativeArgumentsForAction("click", { ref: "e2", human: true })).toEqual(["click", "@e2", "--human"]);
		expect(nativeArgumentsForAction("dblclick", { ref: "e2", human: true })).toEqual(["dblclick", "@e2"]);
		expect(nativeArgumentsForAction("drag", { ref: "e2", targetRef: "@e5", human: true })).toEqual([
			"drag",
			"@e2",
			"@e5",
			"--human",
		]);
		expect(nativeArgumentsForAction("drag", { ref: "e2", targetRef: "@e5" })).toEqual([
			"drag",
			"@e2",
			"@e5",
		]);
	});

	it("maps waits, tabs, frames, and dialogs without accepting arbitrary evaluation", () => {
		expect(nativeArgumentsForAction("wait", { textGone: "Saving...", timeoutMs: 2_500 })).toEqual([
			"wait",
			"text=Saving...",
			"--state",
			"hidden",
			"--timeout",
			"2500",
		]);
		expect(nativeArgumentsForAction("tab-select", { tabId: "t2" })).toEqual(["tab", "t2"]);
		expect(nativeArgumentsForAction("get", { property: "text" })).toEqual(["get", "text"]);
		const stableWait = nativeArgumentsForAction("wait", { stableMs: 750, timeoutMs: 2_500 });
		expect(stableWait.slice(0, 2)).toEqual(["wait", "--fn"]);
		expect(stableWait[2]).toContain("750");
		expect(stableWait.slice(-2)).toEqual(["--timeout", "2500"]);
		expect(nativeArgumentsForAction("frame", { target: "e7" })).toEqual(["frame", "@e7"]);
		expect(nativeArgumentsForAction("dialog", { operation: "accept", text: "yes" })).toEqual([
			"dialog",
			"accept",
			"yes",
		]);
		expect(() => nativeArgumentsForAction("eval", { expression: "document.cookie" })).toThrow();
	});
});
