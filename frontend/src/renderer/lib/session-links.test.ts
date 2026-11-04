import { describe, expect, it } from "vitest";
import { artifactLinkForPath, findSessionLinks, isTallpineLink, parseArtifactLink, parseSessionLink, resolveSessionLink } from "./session-links";

describe("session links", () => {
	it.each([
		["tallpine://sessions/project/session", { projectId: "project", sessionId: "session" }],
		["tallpine://sessions/project%20one/session%2Done", { projectId: "project one", sessionId: "session-one" }],
	])("parses %s", (url, expected) => expect(parseSessionLink(url)).toEqual(expected));

	it.each([
		"Tallpine://sessions/project/session", "a0://sessions/project/session", "tallpine://session/project/session",
		"tallpine://sessions/project", "tallpine://sessions/project/session/extra", "tallpine://sessions//session",
		"tallpine://sessions/project/session?kill=true", "tallpine://sessions/project/session#chat",
		"tallpine://user@sessions/project/session", "tallpine://sessions/project/%ZZ", "tallpine://sessions/project/%2Faction",
		"tallpine://sessions/project/session/kill", "tallpine://sessions/%20/%20", "tallpine://sessions/project/%09",
	])("rejects unsupported value %s", (url) => expect(parseSessionLink(url)).toBeUndefined());

	it("trims adjacent punctuation while preserving encoded IDs", () => {
		expect(findSessionLinks("See (tallpine://sessions/p/s), then tallpine://sessions/p/t.")).toEqual([
			{ start: 5, end: 28, text: "tallpine://sessions/p/s" },
			{ start: 36, end: 59, text: "tallpine://sessions/p/t" },
		]);
	});

	it.each([
		['"tallpine://sessions/proj/sess"', "tallpine://sessions/proj/sess"],
		["'tallpine://sessions/proj/sess'", "tallpine://sessions/proj/sess"],
		["[tallpine://sessions/proj/sess]", "tallpine://sessions/proj/sess"],
		["«tallpine://sessions/proj/sess»", "tallpine://sessions/proj/sess"],
		["<tallpine://sessions/proj/sess>", "tallpine://sessions/proj/sess"],
		["&lt;tallpine://sessions/proj/sess&gt;", "tallpine://sessions/proj/sess"],
		['"tallpine://sessions/proj/sess").', "tallpine://sessions/proj/sess"],
	])("does not include a wrapping delimiter in %s", (text, expected) => {
		expect(findSessionLinks(text)).toEqual([
			{ start: text.indexOf("tallpine://"), end: text.indexOf("tallpine://") + expected.length, text: expected },
		]);
	});

	it("resolves by stable project and session IDs with termination state", () => {
		const workspaces = [{ id: "p", sessions: [{ id: "s", title: "renamed", status: "terminated" }] }];
		expect(resolveSessionLink("tallpine://sessions/p/s", workspaces)).toEqual({ projectId: "p", sessionId: "s", isTerminated: true });
		expect(resolveSessionLink("tallpine://sessions/p/missing", workspaces)).toBeUndefined();
	});

	it.each([
		["tallpine://sessions/p/s/artifacts/plan.md", { projectId: "p", sessionId: "s", path: "plan.md" }],
		["tallpine://sessions/p/s/artifacts/plans/my%20plan.md", { projectId: "p", sessionId: "s", path: "plans/my plan.md" }],
	])("parses artifact link %s", (url, expected) => {
		expect(parseArtifactLink(url)).toEqual(expected);
		expect(parseSessionLink(url)).toBeUndefined();
		expect(isTallpineLink(url)).toBe(true);
	});

	it.each([
		"tallpine://sessions/p/s/artifacts", "tallpine://sessions/p/s/artifacts/", "tallpine://sessions/p/s/files/plan.md",
		"tallpine://sessions/p/s/artifacts/../secret", "tallpine://sessions/p/s/artifacts/a/%2E%2E/b", "tallpine://sessions/p/s/artifacts/a%2Fb",
		"tallpine://sessions/p/s/artifacts/a%5Cb", "tallpine://sessions/p/s/artifacts/plan.md?x=1", "tallpine://sessions/p/s/artifacts/plan.md#top",
		"tallpine://sessions/p/s/artifacts/%ZZ",
	])("rejects unsupported artifact link %s", (url) => expect(parseArtifactLink(url)).toBeUndefined());

	it("finds raw artifact links and resolves them to their session", () => {
		expect(findSessionLinks("See tallpine://sessions/p/s/artifacts/plan.md.")).toEqual([
			{ start: 4, end: 45, text: "tallpine://sessions/p/s/artifacts/plan.md" },
		]);
		const workspaces = [{ id: "p", sessions: [{ id: "s", title: "worker", status: "terminated" }] }];
		expect(resolveSessionLink("tallpine://sessions/p/s/artifacts/plan.md", workspaces)).toEqual({ projectId: "p", sessionId: "s", isTerminated: true });
	});

	it("links paths naming a listed artifact, by session-relative or artifact-directory path", () => {
		const workspaces = [{ id: "p 1", sessions: [{ id: "w", artifactFiles: [{ path: "plan.md" }, { path: "plans/my plan.md" }] }] }];
		expect(artifactLinkForPath("w/plan.md", workspaces)).toBe("tallpine://sessions/p%201/w/artifacts/plan.md");
		expect(artifactLinkForPath("w/plans/my plan.md", workspaces)).toBe("tallpine://sessions/p%201/w/artifacts/plans/my%20plan.md");
		expect(artifactLinkForPath(" /tmp/tallpine data/artifacts/w/plans/my plan.md ", workspaces)).toBe("tallpine://sessions/p%201/w/artifacts/plans/my%20plan.md");
		expect(artifactLinkForPath("w/plans/my  plan.md", workspaces)).toBeUndefined();
		expect(artifactLinkForPath("/tmp/tallpine/artifacts/w/plan.md", workspaces)).toBe("tallpine://sessions/p%201/w/artifacts/plan.md");
		expect(artifactLinkForPath("C:\\tallpine\\artifacts\\w\\plan.md", workspaces)).toBe("tallpine://sessions/p%201/w/artifacts/plan.md");
		expect(artifactLinkForPath("w/missing.md", workspaces)).toBeUndefined();
		expect(artifactLinkForPath("/tmp/w/plan.md", workspaces)).toBeUndefined();
		expect(artifactLinkForPath("plan.md", workspaces)).toBeUndefined();
	});
});

describe("tallpine:// links", () => {
	it("parses session and artifact routes under either scheme", () => {
		expect(parseSessionLink("tallpine://sessions/project/session")).toEqual({ projectId: "project", sessionId: "session" });
		expect(parseArtifactLink("tallpine://sessions/p/s/artifacts/a/b.md")).toEqual({ projectId: "p", sessionId: "s", path: "a/b.md" });
		expect(isTallpineLink("tallpine://sessions/p/s")).toBe(true);
		expect(isTallpineLink("tallpine://sessions/p/s")).toBe(true);
		expect(isTallpineLink("tallpine://other/p/s")).toBe(false);
		expect(isTallpineLink("tallpine://sessions/p/s?x=1")).toBe(false);
	});

	it("finds raw links of both schemes in text", () => {
		const text = "see tallpine://sessions/p/s-1. and tallpine://sessions/p/s-2!";
		expect(findSessionLinks(text).map((l) => l.text)).toEqual(["tallpine://sessions/p/s-1", "tallpine://sessions/p/s-2"]);
	});
});
