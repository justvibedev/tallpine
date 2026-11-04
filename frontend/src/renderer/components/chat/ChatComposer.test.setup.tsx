import { render as rtlRender, screen, type RenderResult } from "@testing-library/react";
import type { ReactElement } from "react";
import { vi } from "vitest";
import { ChatComposer } from "./ChatComposer";
import { TooltipProvider } from "../ui/tooltip";
import type { ChatSkill } from "../../types/conversation";
import { type ChatDraftExcerptReference } from "../../lib/chat-drafts";
import { lexicalEditorText, typeInLexicalEditor } from "../../test/lexical";

// Every button in the composer relies on the shared styled Tooltip, which needs
// a TooltipProvider ancestor. `wrapper` survives `rerender`, so every render call
// in this file — direct or through renderComposer/renderSteerable — gets one.
export function render(ui: ReactElement, options?: Parameters<typeof rtlRender>[1]): RenderResult {
	return rtlRender(ui, { wrapper: TooltipProvider, ...options });
}


export const SKILLS: ChatSkill[] = [
	{ name: "code-review", displayName: "code-review", description: "Review the diff", source: "user" },
	{ name: "review", displayName: "review", description: "Look it over", source: "repo" },
	{ name: "ship", displayName: "ship", description: "Open a PR", source: "user" },
];


export const EXCERPTS: ChatDraftExcerptReference[] = [
	{ id: "excerpt", conversationId: "conversation-1", messageId: "source-1", revision: 1, text: "selected context", role: "assistant" },
];


export const FILES = [
	"AGENTS.md",
	"backend/internal/ports/chat.go",
	"frontend/src/renderer/components/chat/ChatComposer.tsx",
];


export function renderComposer(props: Partial<Parameters<typeof ChatComposer>[0]> = {}) {
	const onSend = vi.fn();
	render(<ChatComposer onSend={onSend} {...props} />);
	return { onSend, field: screen.getByLabelText("Message the agent") as HTMLElement };
}


export function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((resolvePromise) => {
		resolve = resolvePromise;
	});
	return { promise, resolve };
}


export async function typeInComposer(field: HTMLElement, text: string) {
	await typeInLexicalEditor(field, text);
}


export function composerWireText(field: HTMLElement): string {
	return lexicalEditorText(field);
}


export function clipboardData(files: File[]) {
	return {
		files,
		items: [],
		getData: () => "",
		setData: () => undefined,
	};
}


export const png = (name = "shot.png") =>
	new File([new Uint8Array([137, 80, 78, 71])], name, { type: "image/png" });

export const textFile = (name = "notes.txt") => new File(["hello"], name, { type: "text/plain" });

/* ---- the keyboard contract the composer already had ---------------------- */
