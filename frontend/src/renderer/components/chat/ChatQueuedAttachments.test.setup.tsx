// Shared by the queued-attachment suites: a ChatWorkspace with one queued turn to edit.
import { PLACEHOLDER_BLOCK_HEADER } from "@tallpine/shared/chat/image-placeholders";
import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { vi } from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { chatFixture } from "../../lib/chat-fixture";
import { TooltipProvider } from "../ui/tooltip";
import type { ConversationContentSummary, ConversationSnapshot } from "../../types/conversation";

export const path = ".tallpine/attachments/attachment-shot.png";
// What older builds wrote; reading it must still work.
export const suffix = `Attached files (read these files in the workspace):\n- ${path}`;
export const sent = `---\n${PLACEHOLDER_BLOCK_HEADER}\n- [IMG1] ${path}`;

export function setup(text = "inspect this", content: ConversationContentSummary[] = [], nativeImages = true) {
	const edit = vi.fn().mockResolvedValue(undefined);
	const send = vi.fn().mockResolvedValue(undefined);
	const stage = vi.fn().mockResolvedValue([path]);
	const cancel = vi.fn().mockResolvedValue(undefined);
	const snapshot: ConversationSnapshot = {
		...chatFixture,
		turns: [{ id: "q1", state: "queued" as const, requestedAt: "2026-09-06T10:00:00Z" }],
		items: [
			{
				kind: "message" as const,
				id: "m1",
				turnId: "q1",
				sequence: 1,
				revision: 0,
				role: "user" as const,
				origin: "human" as const,
				text,
				content,
				streaming: false,
				createdAt: "2026-09-06T10:00:00Z",
			},
		],
	};
	const view = render(
		<TooltipProvider>
			<ChatWorkspace
				snapshot={snapshot}
				onSend={send}
				onEditQueuedTurn={edit}
				onCancelQueuedTurn={cancel}
				onStageAttachments={stage}
				nativeImages={nativeImages}
			/>
		</TooltipProvider>,
	);
	return {
		unmount: view.unmount,
		edit,
		send,
		stage,
		cancel,
		snapshot,
		field: screen.getByRole("combobox"),
		rerenderSnapshot: (next: ConversationSnapshot) =>
			view.rerender(
				<TooltipProvider>
					<ChatWorkspace
						snapshot={next}
						onSend={send}
						onEditQueuedTurn={edit}
						onCancelQueuedTurn={cancel}
						onStageAttachments={stage}
						nativeImages={nativeImages}
					/>
				</TooltipProvider>,
			),
	};
}

export async function beginEdit() {
	await userEvent.click(
		within(screen.getByTestId("queued-message-q1")).getByRole("button", { name: "Edit queued message" }),
	);
}

export async function pasteImage(field: HTMLElement, name = "shot.png") {
	fireEvent.paste(field, {
		clipboardData: {
			files: [new File([new Uint8Array([137, 80, 78, 71])], name, { type: "image/png" })],
			items: [],
		},
	});
	await screen.findByLabelText(`Remove ${name}`);
}
