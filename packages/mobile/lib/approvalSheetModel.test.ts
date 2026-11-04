import { describe, expect, it } from "vitest";
import { approvalSheetHref, approvalSheetModel, approvalSheetShouldOpenSession } from "./approvalSheetModel";
import type { ConversationActivity, ConversationSnapshot } from "./chat/types";

const approval = (over: Partial<ConversationActivity> = {}): ConversationActivity =>
	({
		kind: "activity",
		activityKind: "approval",
		status: "pending",
		sequence: 3,
		requestId: "req-1",
		summary: "run a command",
		detail: { command: "rm -rf build", rawCommand: "bash -lc 'rm -rf build'", cwd: "/repo", reason: "Clean the output" },
		decisions: [{ id: "allow", label: "Allow" }, { id: "deny", label: "Deny" }],
		...over,
	}) as ConversationActivity;
const snap = (...items: ConversationActivity[]) => ({ items, turns: [] }) as unknown as ConversationSnapshot;

describe("approvalSheetModel", () => {
	it("shows the exact command that will run, with cwd, reason and the daemon's decisions", () => {
		expect(approvalSheetModel(snap(approval()))).toEqual({
			requestId: "req-1",
			command: "bash -lc 'rm -rf build'",
			cwd: "/repo",
			reason: "Clean the output",
			decisions: [{ id: "allow", label: "Allow" }, { id: "deny", label: "Deny" }],
		});
	});
	it("falls back to the displayed command, then the summary", () => {
		expect(approvalSheetModel(snap(approval({ detail: { command: "ls" } })))?.command).toBe("ls");
		expect(approvalSheetModel(snap(approval({ detail: undefined })))?.command).toBe("run a command");
	});
	it("has nothing to answer without a pending prompt, request id or decisions", () => {
		expect(approvalSheetModel(undefined)).toBeNull();
		expect(approvalSheetModel(snap())).toBeNull();
		expect(approvalSheetModel(snap(approval({ status: "resolved" } as Partial<ConversationActivity>)))).toBeNull();
		expect(approvalSheetModel(snap(approval({ requestId: undefined })))).toBeNull();
		expect(approvalSheetModel(snap(approval({ decisions: [] })))).toBeNull();
	});
});

describe("approvalSheetHref", () => {
	it("turns a session target into the sheet, and leaves other targets alone", () => {
		expect(approvalSheetHref("/session/w-1?hostId=h%201")).toBe("/sheets/approval?sessionId=w-1&hostId=h%201");
		expect(approvalSheetHref("/prs?hostId=h")).toBeNull();
		expect(approvalSheetHref("/")).toBeNull();
	});
});

describe("approvalSheetShouldOpenSession", () => {
	const base = { model: undefined, hasConfig: false, hostLoading: false, hostKnown: false, waitedOut: false };
	const model = approvalSheetModel(snap(approval()));
	it("waits for a computer that has not hydrated instead of skipping the sheet", () => {
		expect(approvalSheetShouldOpenSession(base)).toBe(false);
		expect(approvalSheetShouldOpenSession({ ...base, hostKnown: true, hostLoading: true })).toBe(false);
		expect(approvalSheetShouldOpenSession({ ...base, hasConfig: true })).toBe(false);
		expect(approvalSheetShouldOpenSession({ ...base, hasConfig: true, model })).toBe(false);
	});
	it("opens the session when there is nothing to answer or the computer never arrives", () => {
		expect(approvalSheetShouldOpenSession({ ...base, hasConfig: true, model: null })).toBe(true);
		expect(approvalSheetShouldOpenSession({ ...base, hostKnown: true })).toBe(true);
		expect(approvalSheetShouldOpenSession({ ...base, waitedOut: true })).toBe(true);
	});
});
