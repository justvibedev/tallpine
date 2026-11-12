import { PLACEHOLDER_BLOCK_HEADER } from "@tallpine/shared/chat/image-placeholders";
import {
	render,
	SKILLS,
	FILES,
	renderComposer,
	typeInComposer,
	composerWireText,
	clipboardData,
	png,
	textFile,
} from "./ChatComposer.test.setup";

import { purgeFileAttachmentsForSession } from "../../hooks/useFileAttachments";
import {
	act,
	fireEvent,
	screen,
	waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
	describe,
	expect,
	it,
	vi,
} from "vitest";
import { ChatComposer } from "./ChatComposer";
import { prepareChatComposerDelivery, readChatSessionDraft } from "../../lib/chat-drafts";
import { getChatDraftBoundaries, getChatDraftBoundary } from "../../lib/chat-draft-boundary";
import { placeLexicalCaret, typeAndPressInLexicalEditor } from "../../test/lexical";



describe("slash commands", () => {
	it("opens the skill menu on a leading slash", async () => {
		const { field } = renderComposer({ skills: SKILLS });
		await typeInComposer(field, "/");
		expect(screen.getByRole("listbox")).toBeTruthy();
		expect(screen.getAllByRole("option")).toHaveLength(3);
	});

	it("hides the generic agent source and keeps the Tallpine source label", async () => {
		const { field } = renderComposer({
			skills: [
				{ name: "built-in", displayName: "built-in", source: "agent" },
				{ name: "compact", displayName: "compact", source: "ao" },
			],
		});
		await typeInComposer(field, "/");

		expect(screen.queryByText("agent", { exact: true })).toBeNull();
		expect(screen.getByText("Tallpine", { exact: true })).toBeInTheDocument();
	});

	it("filters as the user types", async () => {
		const { field } = renderComposer({ skills: SKILLS });
		await typeInComposer(field, "/rev");
		const options = screen.getAllByRole("option");
		expect(options).toHaveLength(2);
		// The prefix match leads; the mid-name match follows.
		expect(options[0]?.textContent).toContain("/review");
	});

	// The whole point of gating on the provider's answer: with nothing to offer,
	// the menu must not appear and the slash must behave like a character.
	it("leaves the slash an ordinary character when the provider reported no skills", async () => {
		const { onSend, field } = renderComposer({ skills: [] });
		await typeInComposer(field, "/rev");
		expect(screen.queryByRole("listbox")).toBeNull();
		expect(field.textContent).toBe("/rev");
		// And Enter still sends, rather than being consumed by a menu that is not there.
		await userEvent.keyboard("{Enter}");
		expect(onSend).toHaveBeenCalledWith("/rev");
	});

	it("opens on a slash after existing text", async () => {
		const { field } = renderComposer({ skills: SKILLS });
		await typeInComposer(field, "look here /rev");
		expect(screen.getByRole("listbox")).toBeInTheDocument();
		expect(screen.getByRole("option", { name: /\/review/ })).toBeInTheDocument();
	});

	it("moves the highlight with the arrow keys and wraps", async () => {
		const { field } = renderComposer({ skills: SKILLS });
		await typeInComposer(field, "/");

		const selected = () =>
			screen.getAllByRole("option").findIndex((node) => node.getAttribute("aria-selected") === "true");
		expect(selected()).toBe(0);
		await userEvent.keyboard("{ArrowDown}");
		expect(selected()).toBe(1);
		await userEvent.keyboard("{ArrowUp}{ArrowUp}");
		expect(selected()).toBe(2);
	});

	it("does not let mouse movement change keyboard selection", async () => {
		const { field } = renderComposer({ skills: SKILLS });
		await typeInComposer(field, "/");
		await userEvent.keyboard("{ArrowDown}");
		fireEvent.mouseEnter(screen.getAllByRole("option")[0]!);

		const selected = screen
			.getAllByRole("option")
			.findIndex((node) => node.getAttribute("aria-selected") === "true");
		expect(selected).toBe(1);
	});

	it("returns to the first result when the search changes", async () => {
		const { field } = renderComposer({ skills: SKILLS });
		await typeInComposer(field, "/");
		await userEvent.keyboard("{ArrowDown}{ArrowDown}");
		await typeInComposer(field, "r");

		const selected = screen
			.getAllByRole("option")
			.findIndex((node) => node.getAttribute("aria-selected") === "true");
		expect(selected).toBe(0);
	});

	it("does not force-scroll when filtering keeps the visible first result selected", async () => {
		const scrollIntoView = vi.spyOn(Element.prototype, "scrollIntoView");
		const { field } = renderComposer({ skills: SKILLS });
		await typeInComposer(field, "/");
		scrollIntoView.mockClear();
		await typeInComposer(field, "r");

		expect(scrollIntoView).not.toHaveBeenCalled();
		scrollIntoView.mockRestore();
	});

	it("inserts the highlighted skill on Enter instead of sending", async () => {
		const { onSend, field } = renderComposer({ skills: SKILLS });
		await typeInComposer(field, "/rev");
		await userEvent.keyboard("{Enter}");

		expect(onSend).not.toHaveBeenCalled();
		const token = field.querySelector('[data-composer-token="skill"]');
		expect(token).toHaveTextContent("/review");
		expect(token).toHaveClass("text-logo-accent");
		expect(token).not.toHaveClass("text-accent");
		expect(field.textContent).toBe("/review ");
		// The trigger is finished, so the menu closes rather than re-filtering what was
		// just inserted.
		expect(screen.queryByRole("listbox")).toBeNull();
	});

	it("inserts instead of sending when Enter follows typing before React rerenders", async () => {
		const { onSend, field } = renderComposer({ skills: SKILLS });
		await typeAndPressInLexicalEditor(field, "/rev", "Enter");

		expect(onSend).not.toHaveBeenCalled();
		await waitFor(() => {
			expect(field.querySelector('[data-composer-token="skill"]')).toHaveTextContent("/review");
		});
		expect(field.querySelectorAll('[data-composer-token="skill"]')).toHaveLength(1);
	});

	it("does not complete or send when Enter confirms IME composition", async () => {
		const { onSend, field } = renderComposer({ skills: SKILLS });
		await typeInComposer(field, "/rev");
		fireEvent.keyDown(field, { key: "Enter", isComposing: true });

		expect(onSend).not.toHaveBeenCalled();
		expect(field.querySelector('[data-composer-token="skill"]')).toBeNull();
	});

	it("does not auto-complete an exact skill during IME composition", async () => {
		const onSend = vi.fn();
		const view = render(<ChatComposer onSend={onSend} skills={[]} />);
		const field = screen.getByLabelText("Message the agent");
		await typeInComposer(field, "/ship");
		fireEvent.compositionStart(field);
		view.rerender(<ChatComposer onSend={onSend} skills={SKILLS} />);

		expect(field.querySelector('[data-composer-token="skill"]')).toBeNull();
		fireEvent.compositionEnd(field);

		await waitFor(() => {
			expect(field.querySelector('[data-composer-token="skill"]')).toHaveTextContent("/ship");
		});
	});

	it("keeps Shift+Enter as a newline while the skill menu is open", async () => {
		const { onSend, field } = renderComposer({ skills: SKILLS });
		await typeInComposer(field, "/rev");
		await userEvent.keyboard("{Shift>}{Enter}{/Shift}");

		expect(onSend).not.toHaveBeenCalled();
		expect(field.querySelector('[data-composer-token="skill"]')).toBeNull();
		expect(composerWireText(field)).toBe("/rev\n");
	});

	it("turns a fully typed unambiguous skill into a chip and closes the menu", async () => {
		const { field } = renderComposer({ skills: SKILLS });
		await typeInComposer(field, "/ship");

		await waitFor(() => {
			expect(field.querySelector('[data-composer-token="skill"]')).toHaveTextContent("/ship");
		});
		expect(screen.queryByRole("listbox")).toBeNull();
	});

	it("keeps an exact skill editable when it prefixes another skill", async () => {
		const { field } = renderComposer({
			skills: [
				{ name: "review", displayName: "review", source: "user" },
				{ name: "review-pr", displayName: "review-pr", source: "user" },
			],
		});
		await typeInComposer(field, "/review");

		expect(field.querySelector('[data-composer-token="skill"]')).toBeNull();
		expect(screen.getByRole("listbox")).toBeInTheDocument();
		await typeInComposer(field, "-pr");
		await waitFor(() => {
			expect(field.querySelector('[data-composer-token="skill"]')).toHaveTextContent("/review-pr");
		});
	});

	it("does not duplicate existing whitespace after an accepted completion", async () => {
		const { field } = renderComposer({ skills: SKILLS });
		await typeInComposer(field, "/rev x");
		await placeLexicalCaret(field, 4);
		await userEvent.keyboard("{Enter}");

		expect(composerWireText(field)).toBe("/review x");
	});

	it("sends a skill chip as its plain slash command", async () => {
		const { onSend, field } = renderComposer({ skills: SKILLS });
		await typeInComposer(field, "/rev");
		await userEvent.keyboard("{Enter}{Enter}");

		expect(onSend).toHaveBeenCalledWith("/review");
	});

	it("inserts on Tab as well", async () => {
		const { field } = renderComposer({ skills: SKILLS });
		await typeInComposer(field, "/ship");
		await userEvent.keyboard("{Tab}");
		expect(field.querySelector('[data-composer-token="skill"]')).toHaveTextContent("/ship");
		expect(field.textContent).toBe("/ship ");
	});

	it("closes on Escape and leaves the typed text alone", async () => {
		const { field } = renderComposer({ skills: SKILLS });
		await typeInComposer(field, "/rev");
		await userEvent.keyboard("{Escape}");
		expect(screen.queryByRole("listbox")).toBeNull();
		expect(field.textContent).toBe("/rev");
	});

	// After a dismissal the composer must behave like a plain field again, or Enter
	// would appear to do nothing.
	it("sends on Enter after the menu was dismissed", async () => {
		const { onSend, field } = renderComposer({ skills: SKILLS });
		await typeInComposer(field, "/rev");
		await userEvent.keyboard("{Escape}");
		await userEvent.keyboard("{Enter}");
		expect(onSend).toHaveBeenCalledWith("/rev");
	});

	it("selects with the mouse", async () => {
		const { field } = renderComposer({ skills: SKILLS });
		await typeInComposer(field, "/");
		await userEvent.click(screen.getByText("/ship"));
		expect(field.querySelector('[data-composer-token="skill"]')).toHaveTextContent("/ship");
	});
});

/* ---- file mentions ------------------------------------------------------- */

describe("file mentions", () => {
	it("opens the file menu on an at-sign", async () => {
		const { field } = renderComposer({ filePaths: FILES });
		await typeInComposer(field, "@chat");
		const options = screen.getAllByRole("option");
		// Both files whose name starts with "chat" match; neither AGENTS.md does.
		expect(options).toHaveLength(2);
		// The row reads as a file name plus where it lives, not as one long path.
		expect(options[0]?.textContent).toContain("chat.go");
		expect(options[0]?.textContent).toContain("backend/internal/ports");
	});

	// The label is a name; what the agent has to resolve is the whole path.
	it("inserts the full path, without the sigil", async () => {
		const { field } = renderComposer({ filePaths: FILES });
		await typeInComposer(field, "look at @ChatComposer");
		await userEvent.keyboard("{Enter}");
		const token = field.querySelector('[data-composer-token="file"]');
		expect(token).toHaveTextContent("ChatComposer.tsx");
		expect(token).toHaveAttribute(
			"data-value",
			"frontend/src/renderer/components/chat/ChatComposer.tsx",
		);
	});

	it("inserts a file instead of sending when Enter follows typing before React rerenders", async () => {
		const { onSend, field } = renderComposer({ filePaths: FILES });
		await typeAndPressInLexicalEditor(field, "@chat", "Enter");

		expect(onSend).not.toHaveBeenCalled();
		await waitFor(() => {
			expect(field.querySelector('[data-composer-token="file"]')).toHaveAttribute(
				"data-value",
				"backend/internal/ports/chat.go",
			);
		});
		expect(field.querySelectorAll('[data-composer-token="file"]')).toHaveLength(1);
	});

	it("keeps Shift+Enter as a newline while the file menu is open", async () => {
		const { onSend, field } = renderComposer({ filePaths: FILES });
		await typeInComposer(field, "@chat");
		await userEvent.keyboard("{Shift>}{Enter}{/Shift}");

		expect(onSend).not.toHaveBeenCalled();
		expect(field.querySelector('[data-composer-token="file"]')).toBeNull();
		expect(composerWireText(field)).toBe("@chat\n");
	});

	it("sends the inserted path verbatim", async () => {
		const { onSend, field } = renderComposer({ filePaths: FILES });
		await typeInComposer(field, "@chat.go");
		await userEvent.keyboard("{Enter}");
		await userEvent.keyboard("{Enter}");
		expect(onSend).toHaveBeenCalledWith("backend/internal/ports/chat.go");
	});

	it("quotes a completed path containing spaces in the agent-facing text", async () => {
		const { onSend, field } = renderComposer({ filePaths: ["docs/product notes.md"] });
		await typeInComposer(field, "read @notes");
		await userEvent.keyboard("{Enter}{Enter}");

		expect(field.querySelector('[data-composer-token="file"]')).not.toBeInTheDocument();
		expect(onSend).toHaveBeenCalledWith('read "docs/product notes.md"');
	});

	it("leaves the at-sign ordinary when there are no paths", async () => {
		const { field } = renderComposer({ filePaths: [] });
		await typeInComposer(field, "@chat");
		expect(screen.queryByRole("listbox")).toBeNull();
		expect(field.textContent).toBe("@chat");
	});

	it("says so when the worktree list was capped", async () => {
		const { field } = renderComposer({ filePaths: FILES, filePathsTruncated: true });
		await typeInComposer(field, "@chat");
		expect(screen.getByText(/Showing part of a large worktree/)).toBeTruthy();
	});
});

/* ---- attachments --------------------------------------------------------- */

describe("attachments", () => {
	// A control that cannot deliver must not be drawn: the fixture preview has no
	// worktree to write into.
	it("offers no attach control when there is nowhere to put the bytes", () => {
		renderComposer();
		expect(screen.queryByLabelText("Attach a file")).toBeNull();
	});

	it("offers the attach control when staging is wired", () => {
		renderComposer({ onStageAttachments: vi.fn() });
		expect(screen.getByLabelText("Attach a file")).toBeTruthy();
	});

	it("shows a removable chip per pasted image", async () => {
		const { field } = renderComposer({
			onStageAttachments: vi.fn().mockResolvedValue([
				".tallpine/attachments/attachment-a.png",
				".tallpine/attachments/attachment-b.png",
			]),
		});
		fireEvent.paste(field, { clipboardData: clipboardData([png("a.png"), png("b.png")]) });

		await waitFor(() => expect(screen.getAllByRole("listitem")).toHaveLength(2));
		expect(screen.getByLabelText("Remove a.png")).toBeTruthy();

		await userEvent.click(screen.getByLabelText("Remove a.png"));
		await waitFor(() => expect(screen.getAllByRole("listitem")).toHaveLength(1));
	});

	it("takes a dropped image as a removable chip and sends its bytes", async () => {
		const stage = vi.fn().mockResolvedValue([".tallpine/attachments/dropped.png"]);
		const { onSend, field } = renderComposer({ onStageAttachments: stage, nativeImages: true });

		fireEvent.drop(field, {
			dataTransfer: { files: [png("dropped.png")], items: [{ kind: "file" }], getData: () => "" },
		});
		await waitFor(() => expect(screen.getAllByRole("listitem")).toHaveLength(1));
		await typeInComposer(field, "look");
		await userEvent.keyboard("{Enter}");

		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
		expect(onSend.mock.calls[0]?.[1]).toEqual([{ mimeType: "image/png", data: expect.any(String) }]);
	});

	it("rejects an oversized pasted image with a clear error and no chip", async () => {
		const stage = vi.fn().mockResolvedValue([".tallpine/attachments/huge.png"]);
		const { field } = renderComposer({ onStageAttachments: stage });
		const huge = png("huge.png");
		Object.defineProperty(huge, "size", { value: 51 * 1024 * 1024 });

		fireEvent.paste(field, { clipboardData: clipboardData([huge]) });

		expect(await screen.findByRole("alert")).toHaveTextContent("Each file must be under 50 MB.");
		expect(screen.queryAllByRole("listitem")).toHaveLength(0);
		expect(stage).not.toHaveBeenCalled();
	});

	it("places a pasted image inline at the caret and drops it with its attachment", async () => {
		const stage = vi.fn().mockResolvedValue([".tallpine/attachments/attachment-a.png"]);
		const { onSend, field } = renderComposer({ onStageAttachments: stage });
		await typeInComposer(field, "compare");
		fireEvent.paste(field, { clipboardData: clipboardData([png("a.png")]) });

		const chip = await waitFor(() => {
			const node = field.querySelector('[data-composer-token="image"]');
			expect(node).not.toBeNull();
			return node as HTMLElement;
		});
		expect(chip).toHaveTextContent("[IMG1]");
		expect(chip).toHaveAttribute("title", "a.png");
		expect(field.textContent).toBe("compare [IMG1] ");

		await userEvent.click(screen.getByLabelText("Remove a.png"));
		await waitFor(() => expect(field.querySelector('[data-composer-token="image"]')).toBeNull());
		expect(field.textContent?.trimEnd()).toBe("compare");
		expect(onSend).not.toHaveBeenCalled();
	});

	// Staging can be slow: the chip holds the paste position while the user types on,
	// and Enter during staging still sends the chip the user saw.
	it("keeps a slow image where it was pasted and sends it when Enter beats staging", async () => {
		let finishStaging!: (paths: string[]) => void;
		const stage = vi.fn(() => new Promise<string[]>((resolve) => { finishStaging = resolve; }));
		const { onSend, field } = renderComposer({ onStageAttachments: stage });
		await typeInComposer(field, "make");
		fireEvent.paste(field, { clipboardData: clipboardData([png("a.png")]) });
		expect(field.querySelector('[data-composer-token="image-pending"]')).not.toBeNull();
		await typeInComposer(field, "shorter");
		await waitFor(() => expect(stage).toHaveBeenCalledTimes(1));

		await userEvent.keyboard("{Enter}");
		finishStaging([".tallpine/attachments/attachment-a.png"]);

		await waitFor(() => expect(onSend).toHaveBeenCalledWith(
			`make [IMG1] shorter\n\n---\n${PLACEHOLDER_BLOCK_HEADER}\n- [IMG1] .tallpine/attachments/attachment-a.png`,
		));
	});

	it("sends a staged path the user typed exactly as written", async () => {
		const { onSend, field } = renderComposer({ onStageAttachments: vi.fn() });
		await typeInComposer(field, "No, look at .tallpine/attachments/attachment-old.png again");
		await userEvent.keyboard("{Enter}");
		await waitFor(() => expect(onSend).toHaveBeenCalledWith("No, look at .tallpine/attachments/attachment-old.png again"));
	});

	it("does not send a chip that undo restored after its image was removed", async () => {
		const stage = vi.fn().mockResolvedValue([".tallpine/attachments/attachment-a.png"]);
		const { onSend, field } = renderComposer({ onStageAttachments: stage });
		await typeInComposer(field, "look");
		fireEvent.paste(field, { clipboardData: clipboardData([png("a.png")]) });
		await screen.findByLabelText("Remove a.png");
		await userEvent.click(screen.getByLabelText("Remove a.png"));
		await waitFor(() => expect(field.querySelector('[data-composer-token="image"]')).toBeNull());

		await userEvent.click(field);
		await userEvent.keyboard("{Control>}z{/Control}");
		await waitFor(() => expect(field.querySelector('[data-composer-token="image"]')).not.toBeNull());
		await userEvent.keyboard("{Enter}");

		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
		expect(onSend.mock.calls[0]?.[0]).toBe("look");
	});

	it("ignores a paste that carries no file", async () => {
		const { field } = renderComposer({ onStageAttachments: vi.fn() });
		fireEvent.paste(field, { clipboardData: clipboardData([]) });
		await waitFor(() => expect(screen.queryByRole("listitem")).toBeNull());
	});

	// The chip has to mean something: the bytes get written and the message names
	// the path the agent can open.
	it("stages the image and names the returned path in the message", async () => {
		const stage = vi.fn().mockResolvedValue([".tallpine/attachments/attachment-ab12cd34ef.png"]);
		const { onSend, field } = renderComposer({ onStageAttachments: stage });

		fireEvent.paste(field, { clipboardData: clipboardData([png()]) });
		await waitFor(() => expect(screen.getAllByRole("listitem")).toHaveLength(1));

		await typeInComposer(field, "what is wrong here");
		await userEvent.keyboard("{Enter}");

		await waitFor(() => expect(stage).toHaveBeenCalledTimes(1));
		expect(stage.mock.calls[0]?.[0]).toEqual([
			{ mimeType: "image/png", data: expect.any(String) },
		]);
		await waitFor(() =>
			expect(onSend).toHaveBeenCalledWith(
				// The chip went in at the caret of the empty draft, ahead of the words.
				`[IMG1] what is wrong here\n\n---\n${PLACEHOLDER_BLOCK_HEADER}\n- [IMG1] .tallpine/attachments/attachment-ab12cd34ef.png`,
			),
		);
		// Consumed, so the next message does not silently resend them.
		await waitFor(() => expect(screen.queryByRole("listitem")).toBeNull());
	});

	it("sends an image with no words, since the reference block carries the request", async () => {
		const stage = vi.fn().mockResolvedValue([".tallpine/attachments/attachment-1.png"]);
		const { onSend, field } = renderComposer({ onStageAttachments: stage });

		fireEvent.paste(field, { clipboardData: clipboardData([png()]) });
		await waitFor(() => expect(screen.getAllByRole("listitem")).toHaveLength(1));
		// Sent from the field with no text typed at all.
		await userEvent.keyboard("{Enter}");

		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
		expect(onSend.mock.calls[0]?.[0]).toBe(
			`---\n${PLACEHOLDER_BLOCK_HEADER}\n- [IMG1] .tallpine/attachments/attachment-1.png`,
		);
	});

	it.each([false, true])("retries an ordinary send with the same ID and native bytes after a lost response (restart: %s)", async (restart) => {
		const sessionId = `composer-native-retry-${restart}`;
		const stage = vi.fn().mockResolvedValue([".tallpine/attachments/native-retry.png"]);
		const onSend = vi.fn().mockRejectedValueOnce(new Error("network request never arrived")).mockResolvedValue(undefined);
		let view = render(<ChatComposer onSend={onSend} draftSessionId={sessionId} nativeImages onStageAttachments={stage} />);
		let field = screen.getByLabelText("Message the agent");
		fireEvent.paste(field, { clipboardData: clipboardData([png("native-retry.png")]) });
		await screen.findByLabelText("Remove native-retry.png");
		await typeInComposer(field, "inspect this image");
		fireEvent.keyDown(field, { key: "Enter" });
		await screen.findByRole("button", { name: "Retry message safely" });
		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
		const first = onSend.mock.calls[0];
		expect(first?.[1]).toEqual([{ mimeType: "image/png", data: "iVBORw==" }]);
		expect(first?.[2]).toEqual(expect.any(String));
		expect(getChatDraftBoundaries(sessionId)).toEqual([]);

		const response = new Response();
		vi.spyOn(response, "blob").mockResolvedValue(new Blob([new Uint8Array([137, 80, 78, 71])], { type: "image/png" }));
		const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(response);
		try {
			if (restart) {
				view.unmount();
				purgeFileAttachmentsForSession(sessionId);
				view = render(<ChatComposer onSend={onSend} draftSessionId={sessionId} nativeImages={false} onStageAttachments={stage} />);
				field = screen.getByLabelText("Message the agent");
			}
			await userEvent.click(screen.getByRole("button", { name: "Retry message safely" }));
			await waitFor(() => expect(onSend).toHaveBeenCalledTimes(2));
			expect(onSend.mock.calls[1]).toEqual(first);
			await waitFor(() => expect(field.textContent).toBe(""));
			expect(stage).toHaveBeenCalledTimes(1);
			if (restart) expect(fetch).toHaveBeenCalledWith(expect.stringContaining("native-retry.png"));
		} finally {
			view.unmount();
			fetch.mockRestore();
		}
	});

	it("also sends native image bytes when the provider negotiated image prompts", async () => {
		const stage = vi.fn().mockResolvedValue([".tallpine/attachments/attachment-native.png"]);
		const { onSend, field } = renderComposer({ onStageAttachments: stage, nativeImages: true });

		fireEvent.paste(field, { clipboardData: clipboardData([png()]) });
		await waitFor(() => expect(screen.getAllByRole("listitem")).toHaveLength(1));
		await typeInComposer(field, "inspect this");
		await userEvent.keyboard("{Enter}");

		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
		expect(onSend.mock.calls[0]?.[0]).toContain(".tallpine/attachments/attachment-native.png");
		expect(onSend.mock.calls[0]?.[1]).toEqual([
			{ mimeType: "image/png", data: expect.any(String) },
		]);
	});

	it("sends an image above the native limit by workspace path", async () => {
		const stage = vi.fn().mockResolvedValue([".tallpine/attachments/large.png"]);
		const { onSend, field } = renderComposer({ onStageAttachments: stage, nativeImages: true });
		const largeImage = png("large.png");
		Object.defineProperty(largeImage, "size", { value: 11 * 1024 * 1024 });

		fireEvent.paste(field, { clipboardData: clipboardData([largeImage]) });
		await waitFor(() => expect(screen.getAllByRole("listitem")).toHaveLength(1));
		await typeInComposer(field, "inspect this");
		await userEvent.keyboard("{Enter}");

		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
		expect(onSend.mock.calls[0]?.[0]).toContain(".tallpine/attachments/large.png");
		expect(onSend.mock.calls[0]?.[1]).toBeUndefined();
	});

	it("stages non-images by path without sending them as native image blocks", async () => {
		const stage = vi.fn().mockResolvedValue([
			".tallpine/attachments/attachment-native.png",
			".tallpine/attachments/notes.txt",
		]);
		const { onSend, field } = renderComposer({ onStageAttachments: stage, nativeImages: true });

		fireEvent.drop(field, {
			dataTransfer: {
				files: [png(), textFile()],
				items: [{ kind: "file" }, { kind: "file" }],
				getData: () => "",
			},
		});
		await waitFor(() => expect(screen.getAllByRole("listitem")).toHaveLength(2));
		await typeInComposer(field, "inspect these");
		await userEvent.keyboard("{Enter}");

		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
		expect(onSend.mock.calls[0]?.[0]).toContain(".tallpine/attachments/notes.txt");
		expect(onSend.mock.calls[0]?.[1]).toEqual([
			{ mimeType: "image/png", data: expect.any(String) },
		]);
	});

	// A message claiming an attachment the agent cannot open is worse than a refusal.
	it("does not accept a chip when durable staging fails, and says so", async () => {
		const stage = vi.fn().mockRejectedValue(new Error("disk full"));
		const { onSend, field } = renderComposer({ onStageAttachments: stage });

		fireEvent.paste(field, { clipboardData: clipboardData([png()]) });
		expect(await screen.findByRole("alert")).toHaveTextContent(
			"Files couldn’t be saved. Nothing was attached.",
		);
		expect(stage).toHaveBeenCalledTimes(1);
		expect(onSend).not.toHaveBeenCalled();
		expect(screen.queryByRole("listitem")).not.toBeInTheDocument();
		expect(field.textContent).toBe("");
	});

	it("keeps attachments after a failed send and reuses their staged paths on retry", async () => {
		const stage = vi.fn().mockResolvedValue([".tallpine/attachments/attachment-retry.png"]);
		const onSend = vi.fn().mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce(undefined);
		render(<ChatComposer onSend={onSend} onStageAttachments={stage} />);
		const field = screen.getByLabelText("Message the agent") as HTMLElement;

		fireEvent.paste(field, { clipboardData: clipboardData([png()]) });
		await waitFor(() => expect(screen.getAllByRole("listitem")).toHaveLength(1));
		await typeInComposer(field, "inspect this");
		await userEvent.keyboard("{Enter}");

		expect(await screen.findByRole("alert")).toHaveTextContent("attachments were kept");
		expect(field.textContent).toBe("[IMG1] inspect this");
		expect(screen.getAllByRole("listitem")).toHaveLength(1);

		await userEvent.keyboard("{Enter}");
		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(2));
		expect(stage).toHaveBeenCalledTimes(1);
		await waitFor(() => expect(screen.queryByRole("listitem")).not.toBeInTheDocument());
		expect(field.textContent).toBe("");
	});

	it("retains retry attachments while the next draft is typed and removes them after acceptance", async () => {
		const sessionId = "composer-accepted-attachment-send";
		let acceptSend!: () => void;
		const onSend = vi.fn(
			() =>
				new Promise<void>((resolve) => {
					acceptSend = resolve;
				}),
		);
		const common = {
			onSend,
			draftSessionId: sessionId,
			onStageAttachments: vi.fn().mockResolvedValue([
				".tallpine/attachments/attachment-locked.png",
			]),
		};
		const firstView = render(<ChatComposer {...common} />);
		const field = screen.getByLabelText("Message the agent");
		fireEvent.paste(field, { clipboardData: clipboardData([png("locked.png")]) });
		await screen.findByLabelText("Remove locked.png");
		await typeInComposer(field, "send once");
		await userEvent.click(screen.getByRole("button", { name: "Send message" }));
		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));

		expect(screen.queryByLabelText("Remove locked.png")).not.toBeInTheDocument();
		expect(readChatSessionDraft(sessionId).composer.attachments).toEqual([]);
		expect(readChatSessionDraft(sessionId).composer.delivery?.draft?.attachments).toHaveLength(1);
		await waitFor(() => expect(field).toHaveAttribute("contenteditable", "true"));
		await typeInComposer(field, "next draft");
		await act(async () => acceptSend());

		await waitFor(() =>
			expect(readChatSessionDraft(sessionId).composer).toMatchObject({
				text: "next draft",
				attachments: [],
			}),
		);
		await waitFor(() => expect(screen.queryByLabelText("Remove locked.png")).toBeNull());
		firstView.unmount();

		render(<ChatComposer {...common} />);
		expect(screen.getByLabelText("Message the agent")).toHaveTextContent("next draft");
		expect(screen.queryByLabelText("Remove locked.png")).not.toBeInTheDocument();
	});

	it("releases the unsafe boundary before an accepted send appears cleared", async () => {
		const sessionId = "composer-accepted-boundary-order";
		let acceptSend!: () => void;
		const onSend = vi.fn(
			() =>
				new Promise<void>((resolve) => {
					acceptSend = resolve;
				}),
		);
		render(<ChatComposer onSend={onSend} draftSessionId={sessionId} />);
		const field = screen.getByLabelText("Message the agent");
		await typeInComposer(field, "accepted message");
		let boundariesWhenCleared: readonly string[] | undefined;
		const observer = new MutationObserver(() => {
			if (composerWireText(field) === "") {
				boundariesWhenCleared = [...getChatDraftBoundaries(sessionId)];
			}
		});
		observer.observe(field, { childList: true, characterData: true, subtree: true });
		try {
			await userEvent.click(screen.getByRole("button", { name: "Send message" }));
			await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
			await waitFor(() => expect(boundariesWhenCleared).toEqual([]));
			await act(async () => acceptSend());
			await waitFor(() => expect(composerWireText(field)).toBe(""));
			expect({
				boundariesAfterSettlement: getChatDraftBoundaries(sessionId),
				boundariesWhenCleared,
				persistedComposer: readChatSessionDraft(sessionId).composer,
			}).toEqual({
				boundariesAfterSettlement: [],
				boundariesWhenCleared: [],
				persistedComposer: expect.objectContaining({ text: "", attachments: [] }),
			});
		} finally {
			observer.disconnect();
		}
	});

	it("keeps a failed text-durability boundary after attachment persistence succeeds", async () => {
		const sessionId = "composer-text-storage-failure";
		const durableStorage = window.localStorage;
		let failTextWrite = true;
		const storage = {
			getItem: durableStorage.getItem.bind(durableStorage),
			removeItem: durableStorage.removeItem.bind(durableStorage),
			setItem: (key: string, value: string) => {
				if (failTextWrite && key.includes(encodeURIComponent(sessionId))) {
					failTextWrite = false;
					throw new DOMException("full", "QuotaExceededError");
				}
				durableStorage.setItem(key, value);
			},
		} as Storage;
		const localStorage = vi.spyOn(window, "localStorage", "get").mockReturnValue(storage);
		const view = render(
			<ChatComposer
				onSend={vi.fn()}
				draftSessionId={sessionId}
				onStageAttachments={vi.fn().mockResolvedValue([
					".tallpine/attachments/attachment-after-text-failure.png",
				])}
			/>,
		);
		try {
			const field = screen.getByLabelText("Message the agent");
			await typeInComposer(field, "unsafe text");
			await waitFor(() => expect(getChatDraftBoundary(sessionId)).toBe("persistence-failed"));

			fireEvent.paste(field, { clipboardData: clipboardData([textFile("safe-attachment.txt")]) });
			await screen.findByLabelText("Remove safe-attachment.txt");
			expect(getChatDraftBoundary(sessionId)).toBe("persistence-failed");
		} finally {
			view.unmount();
			localStorage.mockRestore();
		}
	});

	// Pasting an image writes its chip into the text, so a later successful write
	// makes the whole text durable again, chip included.
	it("makes text durable again, with the chip, when an image is pasted after a failed text save", async () => {
		const sessionId = "composer-image-after-text-failure";
		const durableStorage = window.localStorage;
		let failTextWrite = true;
		const storage = {
			getItem: durableStorage.getItem.bind(durableStorage),
			removeItem: durableStorage.removeItem.bind(durableStorage),
			setItem: (key: string, value: string) => {
				if (failTextWrite && key.includes(encodeURIComponent(sessionId))) {
					failTextWrite = false;
					throw new DOMException("full", "QuotaExceededError");
				}
				durableStorage.setItem(key, value);
			},
		} as Storage;
		const localStorage = vi.spyOn(window, "localStorage", "get").mockReturnValue(storage);
		const view = render(
			<ChatComposer
				onSend={vi.fn()}
				draftSessionId={sessionId}
				onStageAttachments={vi.fn().mockResolvedValue([".tallpine/attachments/attachment-after-failure.png"])}
			/>,
		);
		try {
			const field = screen.getByLabelText("Message the agent");
			await typeInComposer(field, "unsafe text");
			await waitFor(() => expect(getChatDraftBoundary(sessionId)).toBe("persistence-failed"));

			fireEvent.paste(field, { clipboardData: clipboardData([png("after.png")]) });
			await screen.findByLabelText("Remove after.png");
			await waitFor(() => expect(getChatDraftBoundary(sessionId)).not.toBe("persistence-failed"));
			expect(readChatSessionDraft(sessionId).composer.text).toBe(
				"unsafe text .tallpine/attachments/attachment-after-failure.png ",
			);
		} finally {
			view.unmount();
			localStorage.mockRestore();
		}
	});

	it("reports failed text persistence and pending attachment staging together", async () => {
		const sessionId = "composer-mixed-storage-failure";
		const durableStorage = window.localStorage;
		let failTextWrite = true;
		let finishStaging!: (paths: string[]) => void;
		const storage = {
			getItem: durableStorage.getItem.bind(durableStorage),
			removeItem: durableStorage.removeItem.bind(durableStorage),
			setItem: (key: string, value: string) => {
				if (failTextWrite && key.includes(encodeURIComponent(sessionId))) {
					failTextWrite = false;
					throw new DOMException("full", "QuotaExceededError");
				}
				durableStorage.setItem(key, value);
			},
		} as Storage;
		const localStorage = vi.spyOn(window, "localStorage", "get").mockReturnValue(storage);
		const view = render(
			<ChatComposer
				onSend={vi.fn()}
				draftSessionId={sessionId}
				onStageAttachments={() =>
					new Promise<string[]>((resolve) => {
						finishStaging = resolve;
					})
				}
			/>,
		);
		try {
			const field = screen.getByLabelText("Message the agent");
			await typeInComposer(field, "unsafe text");
			await waitFor(() => expect(getChatDraftBoundary(sessionId)).toBe("persistence-failed"));

			fireEvent.paste(field, { clipboardData: clipboardData([textFile("pending.txt")]) });
			await waitFor(() => expect(finishStaging).toBeTypeOf("function"));
			expect(getChatDraftBoundaries(sessionId)).toEqual([
				"persistence-failed",
				"pending-attachments",
			]);

			await act(async () => finishStaging([".tallpine/attachments/pending.png"]));
		} finally {
			view.unmount();
			localStorage.mockRestore();
		}
	});

	it("keeps a failed attachment-durability boundary after text persistence succeeds", async () => {
		const sessionId = "composer-attachment-storage-failure";
		const durableStorage = window.localStorage;
		let failAttachmentWrite = true;
		const storage = {
			getItem: durableStorage.getItem.bind(durableStorage),
			removeItem: durableStorage.removeItem.bind(durableStorage),
			setItem: (key: string, value: string) => {
				if (failAttachmentWrite && key.includes(encodeURIComponent(sessionId))) {
					failAttachmentWrite = false;
					throw new DOMException("full", "QuotaExceededError");
				}
				durableStorage.setItem(key, value);
			},
		} as Storage;
		const localStorage = vi.spyOn(window, "localStorage", "get").mockReturnValue(storage);
		const view = render(
			<ChatComposer
				onSend={vi.fn()}
				draftSessionId={sessionId}
				onStageAttachments={vi.fn().mockResolvedValue([
					".tallpine/attachments/attachment-storage-failure.png",
				])}
			/>,
		);
		try {
			const field = screen.getByLabelText("Message the agent");
			fireEvent.paste(field, { clipboardData: clipboardData([textFile("unsafe-attachment.txt")]) });
			await screen.findByLabelText("Remove unsafe-attachment.txt");
			await waitFor(() => expect(getChatDraftBoundary(sessionId)).toBe("persistence-failed"));

			await typeInComposer(field, "safely persisted text");
			expect(getChatDraftBoundary(sessionId)).toBe("persistence-failed");
		} finally {
			view.unmount();
			localStorage.mockRestore();
		}
	});

	it.each(["read", "write"] as const)(
		"does not dispatch until the exact composer draft survives a persistent storage %s failure",
		async (failure) => {
			const sessionId = `composer-proof-${failure}-failure`;
			const durableStorage = window.localStorage;
			let storageAvailable = false;
			const storage = {
				getItem: (key: string) => {
					if (!storageAvailable && failure === "read") {
						throw new DOMException("blocked", "SecurityError");
					}
					return durableStorage.getItem(key);
				},
				setItem: (key: string, value: string) => {
					if (!storageAvailable && failure === "write") {
						throw new DOMException("full", "QuotaExceededError");
					}
					durableStorage.setItem(key, value);
				},
				removeItem: durableStorage.removeItem.bind(durableStorage),
			} as Storage;
			const localStorage = vi.spyOn(window, "localStorage", "get").mockReturnValue(storage);
			const onSend = vi.fn(async () => undefined);
			const view = render(<ChatComposer onSend={onSend} draftSessionId={sessionId} />);
			try {
				const field = screen.getByLabelText("Message the agent");
				await typeInComposer(field, "prove this exact draft");
				fireEvent.keyDown(field, { key: "Enter" });

				await waitFor(() =>
					expect(screen.getByRole("alert")).toHaveTextContent("Nothing was sent"),
				);
				expect(onSend).not.toHaveBeenCalled();

				// The error can render before the async submission releases its lock.
				// Retry only once the user-facing send control is available again.
				await waitFor(() => expect(screen.getByRole("button", { name: "Send message" })).toBeEnabled());
				storageAvailable = true;
				fireEvent.keyDown(field, { key: "Enter" });
				await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
				expect(onSend).toHaveBeenCalledWith(
					"prove this exact draft",
					undefined,
					expect.any(String),
				);
			} finally {
				view.unmount();
				localStorage.mockRestore();
			}
		},
	);

	it("re-proves a restored delivery before its explicit retry can reach the API", async () => {
		const sessionId = "composer-restored-proof-failure";
		const durableStorage = window.localStorage;
		prepareChatComposerDelivery(
			sessionId,
			{
				kind: "send",
				composerText: "restored exact draft",
				attachments: [],
				requestText: "restored exact draft",
				clientMessageId: "restored-client-id",
			},
			durableStorage,
		);
		let storageAvailable = false;
		const storage = {
			getItem: durableStorage.getItem.bind(durableStorage),
			setItem: (key: string, value: string) => {
				if (!storageAvailable) throw new DOMException("full", "QuotaExceededError");
				durableStorage.setItem(key, value);
			},
			removeItem: durableStorage.removeItem.bind(durableStorage),
		} as Storage;
		const localStorage = vi.spyOn(window, "localStorage", "get").mockReturnValue(storage);
		const onSend = vi.fn(async () => undefined);
		const view = render(<ChatComposer onSend={onSend} draftSessionId={sessionId} />);
		try {
			await userEvent.click(screen.getByRole("button", { name: "Retry message safely" }));
			await waitFor(() =>
				expect(screen.getByRole("alert")).toHaveTextContent("Nothing was sent"),
			);
			expect(onSend).not.toHaveBeenCalled();

			storageAvailable = true;
			await userEvent.click(screen.getByRole("button", { name: "Retry message safely" }));
			await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
			expect(onSend).toHaveBeenCalledWith(
				"restored exact draft",
				undefined,
				"restored-client-id",
			);
		} finally {
			view.unmount();
			localStorage.mockRestore();
		}
	});
});

/* ---- disabled and busy states ------------------------------------------- */

