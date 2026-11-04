import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { parseViaPrefix, stripSenderPrefix } from "./sender-prefix";
import { foldedReportsEnvelope, humanVisibleText, parseReports, splitArtifacts, splitHeadline } from "./worker-reports";

describe("backend report format contract", () => {
	// Written by the Go test TestRenderMatchesClientGoldenFixture (UPDATE_GOLDEN=1), read here.
	const fixture = readFileSync(resolve(__dirname, "../../testdata/report-render.golden"), "utf8");
	it("parses every entry of the daemon's rendering, without frame markers", () => {
		const updates = parseReports(fixture, "2026-11-01T00:00:00Z", "k");
		expect(updates.map((u) => [u.state, u.name, u.sessionId])).toEqual([
			["done", "Fix login", "app-1"],
			["needs_input", "Docs", "app-2"],
			["stuck", "Docs", "app-2"],
		]);
		expect(updates[0].body).toBe("Opened the PR.\nTests pass.\npr_created: https://github.com/o/r/pull/1\nartifact: notes.md");
		expect(updates[2].body).toBe("[auto] Turn ended.\nLast message: done");
		expect(updates.map((u) => u.body).join("")).not.toMatch(/<<|>>/);
		expect(splitArtifacts(updates[0].body).artifacts).toEqual(["notes.md"]);
	});
});

describe("report entry schemes", () => {
	it.each(["tallpine", "vo", "ao"])("reads %s:// links, named and bare", (scheme) => {
		const text = `Reports since your previous turn:\n\n[done] [Fix](${scheme}://sessions/p/s1)\nok\n\n[stuck] ${scheme}://sessions/p/s2\nno`;
		expect(parseReports(text, "t", "k").map((u) => [u.name, u.sessionId, u.projectId])).toEqual([["Fix", "s1", "p"], ["s2", "s2", "p"]]);
	});
});

describe("sender and via prefixes", () => {
	it("strips a [from] prefix with or without a worker word and name", () => {
		expect(stripSenderPrefix('[from worker app-1 "Fix"] hi', "app-1")).toBe("hi");
		expect(stripSenderPrefix("[from app-1] hi", "app-1")).toBe("hi");
		expect(stripSenderPrefix("[from other] hi", "app-1")).toBe("[from other] hi");
	});
	it("reads [via tool] and [via tool #tag]", () => {
		expect(parseViaPrefix("[via grok-bot] hello")).toEqual({ name: "grok-bot", body: "hello" });
		expect(parseViaPrefix("[via grok-bot #t1]  hello")).toEqual({ name: "grok-bot", body: "hello" });
		expect(parseViaPrefix("plain")).toBeNull();
	});
});

describe("folded reports and headlines", () => {
	it("splits the human text from the folded reports", () => {
		const text = "hi\n\n<ao-worker-reports>\nReports since your previous turn:\n</ao-worker-reports>";
		expect(humanVisibleText(text)).toBe("hi");
		expect(foldedReportsEnvelope(text)).toBe("Reports since your previous turn:");
		expect(foldedReportsEnvelope("hi")).toBe("");
	});
	it("headlines a short first line, else the first sentence", () => {
		expect(splitHeadline("Done.\nmore")).toEqual({ headline: "Done.", detail: "more" });
		expect(splitHeadline("One thing. Two things")).toEqual({ headline: "One thing", detail: "One thing. Two things" });
	});
});
