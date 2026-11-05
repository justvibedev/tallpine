import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ConversationSnapshot } from "../../types/conversation";
import { autoOpensAssistantLinks, useAutoOpenAssistantLinks } from "./useAutoOpenAssistantLinks";

type Item = { id: string; kind: "message"; role: "user" | "assistant"; sequence: number; revision: number; streaming: boolean; text: string };
const msg = (id: string, role: Item["role"], sequence: number, text: string, streaming = false): Item => ({
	id, kind: "message", role, sequence, revision: 1, streaming, text,
});
const snap = (items: Item[]) => ({ items, latestSequence: items.at(-1)?.sequence ?? 0 }) as unknown as ConversationSnapshot;

function mount(initial: ConversationSnapshot, sessionId: string, enabled = true) {
	const open = vi.fn();
	const view = renderHook(
		({ snapshot }) =>
			useAutoOpenAssistantLinks({ snapshot, isLoading: false, uiSessionId: sessionId, paths: [], autoOpenLinkInBrowser: open, enabled }),
		{ initialProps: { snapshot: initial } },
	);
	return { open, ...view };
}

describe("useAutoOpenAssistantLinks", () => {
	it("does not open links from history present when the session first mounts", () => {
		const { open } = mount(snap([msg("a0", "assistant", 1, "see https://old.example.com"), msg("u1", "user", 2, "next")]), "s-history");
		expect(open).not.toHaveBeenCalled();
	});

	it("opens the first link of an assistant reply that finishes streaming, once per session", () => {
		const { open, rerender } = mount(snap([msg("u1", "user", 1, "go")]), "s-stream");
		rerender({ snapshot: snap([msg("u1", "user", 1, "go"), msg("a1", "assistant", 2, "partial https://app.example.com/x.", true)]) });
		expect(open).not.toHaveBeenCalled();
		rerender({ snapshot: snap([msg("u1", "user", 1, "go"), msg("a1", "assistant", 2, "done: https://app.example.com/x.")]) });
		expect(open).toHaveBeenCalledTimes(1);
		expect(open).toHaveBeenCalledWith("https://app.example.com/x");
		rerender({ snapshot: snap([msg("u1", "user", 1, "go"), msg("a1", "assistant", 2, "done: https://app.example.com/x."), msg("a2", "assistant", 3, "also https://two.example.com")]) });
		expect(open).toHaveBeenCalledTimes(1);
	});

	it("opens the current turn's reply that is already complete in the first snapshot", () => {
		const { open } = mount(snap([msg("u1", "user", 1, "go"), msg("a1", "assistant", 2, "ready https://fast.example.com")]), "s-fast");
		expect(open).toHaveBeenCalledWith("https://fast.example.com");
	});

	it("never opens links when disabled (orchestrator chats), on mount or after a reply completes", () => {
		const { open, rerender } = mount(snap([msg("u1", "user", 1, "go"), msg("a1", "assistant", 2, "PR https://github.com/o/private/pull/1")]), "s-off", false);
		expect(open).not.toHaveBeenCalled();
		rerender({ snapshot: snap([msg("u1", "user", 1, "go"), msg("a1", "assistant", 2, "PR https://github.com/o/private/pull/1"), msg("u2", "user", 3, "more"), msg("a2", "assistant", 4, "see https://github.com/o/private/pull/2")]) });
		expect(open).not.toHaveBeenCalled();
	});

	it("auto-opens for worker chats but not orchestrators (by kind or -orchestrator id)", () => {
		expect(autoOpensAssistantLinks({ id: "w-1", kind: "worker" })).toBe(true);
		expect(autoOpensAssistantLinks({ id: "o-1", kind: "orchestrator" })).toBe(false);
		expect(autoOpensAssistantLinks({ id: "proj-orchestrator", kind: "worker" })).toBe(false);
	});
});
