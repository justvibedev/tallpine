import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, expect, it } from "vitest";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { useSideChatsAvailable } from "./side-chats";

const session: WorkspaceSession = {
	id: "chat", workspaceId: "project", workspaceName: "Project", title: "Chat",
	provider: "claude-code", mode: "chat", status: "working", updatedAt: "", prs: [],
};

beforeEach(() => useUiStore.setState({ sideChats: true }));
afterEach(() => useUiStore.setState({ sideChats: false }));

it.each(["codex", "claude-code"] as const)(
	"allows admitted local Chat sessions for %s without a provider allowlist",
	(provider) => {
		const { result } = renderHook(() => useSideChatsAvailable({ ...session, provider }));
		expect(result.current).toBe(true);
	},
);

it("keeps side conversations accessible while their parent is idle", () => {
	const { result } = renderHook(() => useSideChatsAvailable({ ...session, status: "idle" }));
	expect(result.current).toBe(true);
});

const restrictions: Array<Partial<WorkspaceSession> & { host?: string }> = [
	{ host: "remote" }, { mode: "tui" }, { isTerminated: true },
];
it.each(restrictions)("keeps local live Chat restrictions: %j", (restriction) => {
	const { result } = renderHook(() => useSideChatsAvailable({ ...session, ...restriction }, restriction.host));
	expect(result.current).toBe(false);
});

it("requires both developer mode and the side-chat experiment", () => {
	useUiStore.setState({ sideChats: false });
	const { result } = renderHook(() => useSideChatsAvailable(session));
	expect(result.current).toBe(false);
});
