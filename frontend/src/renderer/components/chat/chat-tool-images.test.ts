import { describe, expect, it } from "vitest";
import { toolImagePaths } from "./chat-tool-images";
import type { ConversationActivity } from "../../types/conversation";

function activity(partial: Partial<ConversationActivity> & { detail?: Record<string, unknown> }): ConversationActivity {
	return { kind: "activity", id: "a", sequence: 1, revision: 1, activityKind: "mcp_tool", status: "completed", summary: "tool", createdAt: "2026-01-01T00:00:00Z", ...partial } as ConversationActivity;
}

describe("toolImagePaths", () => {
	it("reads image paths from file changes, tool results and command output", () => {
		expect(toolImagePaths(activity({ activityKind: "file_change", detail: { files: [{ path: "/w/a.png", additions: 0, deletions: 0 }, { path: "/w/b.ts", additions: 1, deletions: 0 }] } }))).toEqual(["/w/a.png"]);
		expect(toolImagePaths(activity({ detail: { result: { path: "/w/shots/s1.png" }, arguments: { path: "/w/in.jpg" } } }))).toEqual(["/w/in.jpg", "/w/shots/s1.png"]);
		expect(toolImagePaths(activity({ activityKind: "command", detail: { output: "wrote /w/out.webp\n" } }))).toEqual(["/w/out.webp"]);
	});

	it("skips relative and parent paths, repeats, and a call still running, and caps at 12", () => {
		expect(toolImagePaths(activity({ detail: { result: "rel/a.png /w/../x.png /w/ok.png /w/ok.png" } }))).toEqual(["/w/ok.png"]);
		expect(toolImagePaths(activity({ status: "running", detail: { result: "/w/ok.png" } }))).toEqual([]);
		const many = Array.from({ length: 20 }, (_, i) => `/w/${i}.png`).join(" ");
		expect(toolImagePaths(activity({ detail: { output: many } }))).toHaveLength(12);
	});
});
