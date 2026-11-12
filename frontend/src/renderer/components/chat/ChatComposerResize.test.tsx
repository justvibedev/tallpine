import { renderComposer, typeInComposer, png } from "./ChatComposer.test.setup";
import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { purgeFileAttachmentsForSession } from "../../hooks/useFileAttachments";
import { readChatSessionDraft } from "../../lib/chat-drafts";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

vi.mock("../../lib/resizeImage", () => ({
	resizeImageIfNeeded: vi.fn(async () => ({
		blob: new Blob([new Uint8Array(4)], { type: "image/jpeg" }),
		width: 1568, height: 1176, originalWidth: 4000, originalHeight: 3000,
	})),
	resizeNote: () => "resized from 4000×3000 to 1568×1176",
}));

describe("ChatComposer oversized images", () => {
	it("sends the downscaled copy and links the staged original in the message", async () => {
		const stage = vi.fn().mockResolvedValue([".tallpine/attachments/small.jpg", ".tallpine/attachments/original.png"]);
		const { onSend, field } = renderComposer({ onStageAttachments: stage, nativeImages: true });

		fireEvent.drop(field, { dataTransfer: { files: [png("big.png")], items: [{ kind: "file" }], getData: () => "" } });
		await waitFor(() => expect(screen.getByTestId("composer-attachment-resized").textContent).toBe("resized from 4000×3000 to 1568×1176"));
		// The downscaled copy and the full original are both staged.
		expect(stage.mock.calls[0]?.[0]).toHaveLength(2);
		expect(stage.mock.calls[0]?.[0][0].mimeType).toBe("image/jpeg");
		expect(stage.mock.calls[0]?.[0][1].mimeType).toBe("image/png");

		await typeInComposer(field, "look");
		await userEvent.keyboard("{Enter}");
		await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
		const text = onSend.mock.calls[0]?.[0] as string;
		// After the separator, the block maps the image to its downscaled copy and full original.
		expect(text.startsWith("look\n\n---\n")).toBe(true);
		expect(text).toContain("- [IMG1] .tallpine/attachments/small.jpg (full original: .tallpine/attachments/original.png)");
		expect(onSend.mock.calls[0]?.[1]).toEqual([{ mimeType: "image/jpeg", data: expect.any(String) }]);
	});

	it("keeps the resize note and original link when the draft is restored", async () => {
		const stage = vi.fn().mockResolvedValue([".tallpine/attachments/small.jpg", ".tallpine/attachments/original.png"]);
		const sessionId = "restore-resize";
		const first = renderComposer({ onStageAttachments: stage, draftSessionId: sessionId });
		fireEvent.drop(first.field, { dataTransfer: { files: [png("big.png")], items: [{ kind: "file" }], getData: () => "" } });
		await waitFor(() => expect(screen.getByTestId("composer-attachment-resized")).toBeTruthy());
		expect(readChatSessionDraft(sessionId).composer.attachments[0]?.resize).toEqual({
			note: "resized from 4000×3000 to 1568×1176",
			originalPath: ".tallpine/attachments/original.png",
			originalBytes: expect.any(Number),
		});
		cleanup();
		purgeFileAttachmentsForSession(sessionId);

		const second = renderComposer({ onStageAttachments: stage, draftSessionId: sessionId });
		expect((await screen.findByTestId("composer-attachment-resized")).textContent).toBe("resized from 4000×3000 to 1568×1176");
		await typeInComposer(second.field, "look");
		await userEvent.keyboard("{Enter}");
		await waitFor(() => expect(second.onSend).toHaveBeenCalledTimes(1));
		expect(second.onSend.mock.calls[0]?.[0]).toContain("full original: .tallpine/attachments/original.png");
	});

	it("lets a dropped folder bubble instead of attaching it", async () => {
		const onWindowDrop = vi.fn();
		window.addEventListener("drop", onWindowDrop);
		const { field } = renderComposer({ onStageAttachments: vi.fn().mockResolvedValue([]) });
		fireEvent.drop(field, { dataTransfer: { files: [png("folder")], items: [{ webkitGetAsEntry: () => ({ isDirectory: true }) }], getData: () => "" } });
		expect(onWindowDrop).toHaveBeenCalledTimes(1);
		expect(screen.queryByTestId("composer-attachments-and-annotations")).toBeNull();
		window.removeEventListener("drop", onWindowDrop);
	});
});
