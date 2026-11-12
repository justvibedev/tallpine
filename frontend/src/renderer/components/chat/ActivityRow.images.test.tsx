import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { tallpineBridge } from "../../lib/bridge";
import { ChatImageSourceProvider } from "./chat-image-source";
import { ActivityRow } from "./ChatTimelineItems/ActivityRow";
import type { ConversationActivity } from "../../types/conversation";

const BASE = "http://127.0.0.1:3101";

function toolActivity(detail: Record<string, unknown>): ConversationActivity {
	return { kind: "activity", id: "t1", sequence: 1, revision: 1, activityKind: "mcp_tool", status: "completed", summary: "browser_screenshot", createdAt: "2026-01-01T00:00:00Z", detail } as ConversationActivity;
}

function renderRow(activity: ConversationActivity) {
	vi.spyOn(tallpineBridge.localPath, "inspect").mockImplementation(async (path) => ({
		ok: true, path, kind: "file", canOpen: true, image: { kind: "workspace", path: path.replace("/w/", "") },
	}));
	return render(
		<ChatImageSourceProvider sessionId="s-1" assetBaseUrl={BASE}>
			<ActivityRow activity={activity} />
		</ChatImageSourceProvider>,
	);
}

afterEach(() => vi.restoreAllMocks());

describe("tool row image thumbnails", () => {
	it("shows thumbnails for images the tool touched and pages through them in the lightbox", async () => {
		const user = userEvent.setup();
		renderRow(toolActivity({ result: { saved: ["/w/one.png", "/w/two.png"] } }));
		await waitFor(() => expect(screen.getAllByRole("img", { hidden: true })).toHaveLength(2));
		for (const image of screen.getAllByRole("img", { hidden: true })) fireEvent.load(image);
		await user.click(screen.getByRole("button", { name: "Open image: two.png" }));
		const dialog = screen.getByRole("dialog");
		expect(within(dialog).getByText("2 of 2")).toBeInTheDocument();
		await user.keyboard("{ArrowLeft}");
		expect(within(dialog).getByText("1 of 2")).toBeInTheDocument();
	});

	it("caps at 12 thumbnails", async () => {
		renderRow(toolActivity({ result: Array.from({ length: 20 }, (_, i) => `/w/s${i}.png`).join(" ") }));
		await waitFor(() => expect(screen.getAllByRole("img", { hidden: true })).toHaveLength(12));
	});

	it("shows none when the call touched no image", () => {
		renderRow(toolActivity({ result: "done" }));
		expect(screen.queryByRole("group", { name: "Images" })).toBeNull();
	});
});
