import { describe, expect, it } from "vitest";
import type { DashboardSession } from "./api";
import { SESSION_STATUSES } from "@tallpine/product-ui/session-models";
import { attentionZone, sessionWantsAttention } from "@tallpine/product-ui/session-presentation";
import { attentionOf, sessionTitle, wantsAttention } from "./sessionStatus";

// Minimal session; each test overrides only the fields it is about.
const session = (over: Partial<DashboardSession> = {}): DashboardSession =>
	({
		id: "proj-7",
		projectId: "proj",
		displayName: null,
		status: null,
		branch: null,
		issueId: null,
		issueTitle: null,
		userPrompt: null,
		summary: null,
		prs: [],
		pr: null,
		...over,
	}) as DashboardSession;

describe("sessionTitle", () => {
	it("prefers displayName, then issueId", () => {
		expect(sessionTitle(session({ displayName: "Fix auth", issueId: "Tallpine-12" }))).toBe("Fix auth");
		expect(sessionTitle(session({ issueId: "Tallpine-12" }))).toBe("Tallpine-12");
	});

	// The id is the only field guaranteed to be present, so it is what a session
	// with nothing else must fall back to.
	it("falls back to the id when nothing is named", () => {
		expect(sessionTitle(session())).toBe("proj-7");
	});

	// The bug this function was rewritten for: " " is truthy, so a `||` chain
	// stopped on it and rendered a card with no title at all.
	it("treats a whitespace-only name as absent", () => {
		expect(sessionTitle(session({ displayName: "   " }))).toBe("proj-7");
		expect(sessionTitle(session({ displayName: "\t\n" , issueId: "Tallpine-12" }))).toBe("Tallpine-12");
	});

	it("trims a name that has content", () => {
		expect(sessionTitle(session({ displayName: "  Fix auth  " }))).toBe("Fix auth");
	});

	// Whatever happens, a card never renders an empty string.
	it("never returns blank", () => {
		for (const s of [
			session(),
			session({ displayName: " " }),
			session({ displayName: " ", issueId: " ", summary: " " }),
		]) {
			expect(sessionTitle(s).length).toBeGreaterThan(0);
		}
	});
});

describe("attentionOf / wantsAttention", () => {
	// status, audience, terminated, bucket, wantsAttention
	const cases: Array<[string, string | undefined, boolean, string, boolean]> = [
		["needs_input", undefined, false, "respond", true],
		["needs_input", "orchestrator", false, "working", false],
		["", undefined, false, "action", true],
		["bogus", undefined, false, "action", true],
		["working", "human", false, "respond", true],
		["ci_failed", undefined, false, "review", true],
		["changes_requested", undefined, false, "review", true],
		["commented", undefined, false, "review", true],
		["exited", undefined, false, "action", true],
		["no_signal", undefined, false, "action", true],
		["merged", undefined, false, "merge", true],
		["mergeable", undefined, false, "merge", true],
		["approved", undefined, false, "merge", true],
		["merged", undefined, true, "done", false],
		["needs_input", undefined, true, "done", false],
		["pr_open", undefined, false, "pending", false],
		["working", undefined, false, "working", false],
		["terminated", undefined, false, "done", false],
	];
	for (const [status, aud, term, bucket, wants] of cases) {
		it(`${status}/${aud ?? "-"}/terminated=${term}`, () => {
			const s = session({ status, needsInputAudience: aud as never, isTerminated: term });
			expect(attentionOf(s)).toBe(bucket);
			expect(wantsAttention(s)).toBe(wants);
		});
	}
	it("matches the product-ui rule for every status x audience x terminated", () => {
		for (const status of SESSION_STATUSES) {
			for (const aud of [undefined, "orchestrator", "human"] as const) {
				for (const isTerminated of [false, true]) {
					const s = session({ status, needsInputAudience: aud, isTerminated });
					const zone = attentionZone({ status, needsInputAudience: aud });
					const expected = isTerminated
						? false
						: zone === "merge" || (zone === "action" && !(status === "needs_input" && aud === "orchestrator"));
					expect(wantsAttention(s)).toBe(expected);
					expect(sessionWantsAttention({ status, needsInputAudience: aud, isTerminated })).toBe(expected);
				}
			}
		}
	});
});
