import { stagedAttachmentParts, withAttachmentReferences } from "@tallpine/shared/chat/attachments";
import { PLACEHOLDER_BLOCK_HEADER } from "@tallpine/shared/chat/image-placeholders";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { renderChat as render } from "../../test/render-chat";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { clipboardData, png, renderComposer, typeInComposer } from "./ChatComposer.test.setup";
import { HumanMessage } from "./ChatTimelineItems";
import { humanMessage } from "./ChatWorkspaceTestHelpers";
import { beginEdit, path as shot, setup } from "./ChatQueuedAttachments.test.setup";
import { typeInLexicalEditor } from "../../test/lexical";

const a = ".tallpine/attachments/attachment-a.png";
const b = ".tallpine/attachments/attachment-b.jpg";
const bOriginal = ".tallpine/attachments/attachment-b0.png";

function chips(field: HTMLElement): string[] {
	return Array.from(field.querySelectorAll('[data-composer-token="image"]'), (chip) => chip.textContent ?? "");
}

describe("composer [IMGn] placeholders", () => {
	it("numbers images in the order they were attached and sends their placeholders where they sit", async () => {
		const stage = vi.fn().mockResolvedValueOnce([a]).mockResolvedValueOnce([b]);
		const { onSend, field } = renderComposer({ onStageAttachments: stage });
		await typeInComposer(field, "compare");
		fireEvent.paste(field, { clipboardData: clipboardData([png("a.png")]) });
		await waitFor(() => expect(chips(field)).toEqual(["[IMG1]"]));
		await typeInComposer(field, "with");
		fireEvent.paste(field, { clipboardData: clipboardData([png("b.png")]) });
		await waitFor(() => expect(chips(field)).toEqual(["[IMG1]", "[IMG2]"]));
		await userEvent.keyboard("{Enter}");

		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
		expect(onSend.mock.calls[0]?.[0]).toBe(
			`compare [IMG1] with [IMG2]\n\n---\n${PLACEHOLDER_BLOCK_HEADER}\n- [IMG1] ${a}\n- [IMG2] ${b}`,
		);
	});

	it("places [IMGn] chips in order even when the draft is empty", async () => {
		const stage = vi.fn().mockResolvedValue([".tallpine/attachments/attachment-a.png", ".tallpine/attachments/attachment-b.png"]);
		const { field } = renderComposer({ onStageAttachments: stage });
		fireEvent.paste(field, { clipboardData: clipboardData([png("a.png"), png("b.png")]) });
		await waitFor(() => expect(screen.getAllByRole("listitem")).toHaveLength(2));
		await waitFor(() => expect(field.querySelectorAll('[data-composer-token="image"]')).toHaveLength(2));
		expect(field.textContent?.trimEnd()).toBe("[IMG1] [IMG2]");
	});

	it("removing an image drops its placeholder and renumbers the rest", async () => {
		const stage = vi.fn().mockResolvedValueOnce([a]).mockResolvedValueOnce([b]);
		const { onSend, field } = renderComposer({ onStageAttachments: stage });
		await typeInComposer(field, "first");
		fireEvent.paste(field, { clipboardData: clipboardData([png("a.png")]) });
		await waitFor(() => expect(chips(field)).toHaveLength(1));
		await typeInComposer(field, "then");
		fireEvent.paste(field, { clipboardData: clipboardData([png("b.png")]) });
		await waitFor(() => expect(chips(field)).toHaveLength(2));

		await userEvent.click(screen.getByLabelText("Remove a.png"));
		await waitFor(() => expect(chips(field)).toEqual(["[IMG1]"]));
		await userEvent.click(field);
		await userEvent.keyboard("{Enter}");

		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
		expect(onSend.mock.calls[0]?.[0]).toBe(`first then [IMG1]\n\n---\n${PLACEHOLDER_BLOCK_HEADER}\n- [IMG1] ${b}`);
	});
});

describe("bubble for an image message", () => {
	it("shows the owner's text with placeholders and the thumbnails in order, and no system text", () => {
		const text = withAttachmentReferences("compare [IMG1] with [IMG2]", [a, b], { [b]: bOriginal });
		const { container } = render(<HumanMessage message={humanMessage(text)} sessionId="tallpine-1" />);

		expect(container.querySelector("[data-chat-message-body]")?.textContent).toBe("compare [IMG1] with [IMG2]");
		const thumbnails = screen.getAllByRole("img");
		expect(thumbnails.map((image) => image.getAttribute("alt"))).toEqual(["Image 1", "Image 2"]);
		expect(thumbnails[0]?.getAttribute("src")).toContain("attachment-a.png");
		expect(thumbnails[1]?.getAttribute("src")).toContain("attachment-b.jpg");
		expect(container.textContent).not.toMatch(/Attached files|full original|---/);
	});

	it("hides the downscaled-images block older messages carry", () => {
		const legacy = `look\n\nAttached files (read these files in the workspace):\n- ${b}` +
			`\n\nDownscaled images (open the original only if the detail matters):\n- ${b} was resized from 4000×3000 to 1568×1176; full original: ${bOriginal}`;
		const { container } = render(<HumanMessage message={humanMessage(legacy)} sessionId="tallpine-1" />);

		expect(container.querySelector("[data-chat-message-body]")?.textContent).toBe("look");
		expect(container.textContent).not.toMatch(/Downscaled images|full original/);
		expect(stagedAttachmentParts(legacy).originals).toEqual({ [b]: bOriginal });
	});
});

describe("queued edit of an image message", () => {
	it("reopens [IMGn] as chips where they sat and keeps each image's full original", async () => {
		const original = ".tallpine/attachments/attachment-shot0.png";
		const { edit } = setup(withAttachmentReferences("inspect [IMG1] closely", [shot], { [shot]: original }));
		// The dock row previews only the owner's words, not the attachment block.
		const row = screen.getByTestId("queued-message-q1").querySelector(".queue-dock-row-text");
		expect(row?.textContent).toBe("inspect [IMG1] closely");
		expect(row?.getAttribute("title")).toBe("inspect [IMG1] closely");
		await beginEdit();
		const field = screen.getByRole("combobox");
		expect(field.querySelector('[data-composer-token="image"]')).toHaveTextContent("[IMG1]");
		expect(field).toHaveTextContent("inspect [IMG1] closely");
		await typeInLexicalEditor(field, " now");
		await userEvent.click(screen.getByRole("button", { name: "Send message" }));
		await waitFor(() => expect(edit).toHaveBeenCalledWith(
			"q1", withAttachmentReferences("inspect [IMG1] closely now", [shot], { [shot]: original }), expect.anything(),
		));
	});
});
