import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { tallpineBridge } from "../../lib/bridge";
import { ChatLocalImageStrip } from "./ChatLocalImageStrip";
import { ChatImageSourceProvider } from "./chat-image-source";
import { AssistantMessage } from "./ChatTimelineItems/AssistantMessage";
import type { ConversationMessage } from "../../types/conversation";
import type { LocalImageSource } from "../../../shared/local-path";

const BASE = "http://127.0.0.1:3101";
const ARTIFACT = "/Users/me/.tallpine/data/artifacts/ranker-6/a light.png";
const WORKSPACE = "/Users/me/.tallpine/data/worktrees/proj/s-1/docs/shot.webp";
const ELSEWHERE = "/Users/me/Desktop/private.png";

// What main answers after checking each path against the session's real roots.
const SOURCES: Record<string, LocalImageSource | undefined> = {
	[ARTIFACT]: { kind: "artifact", sessionId: "ranker-6", path: "a light.png" },
	[WORKSPACE]: { kind: "workspace", path: "docs/shot.webp" },
	[ELSEWHERE]: undefined,
};

function mockMain() {
	return vi.spyOn(tallpineBridge.localPath, "inspect").mockImplementation(async (path) => ({ ok: true, path, kind: "file", canOpen: true, image: SOURCES[path] }));
}

function renderStrip(text: string, remoteHost = false) {
	mockMain();
	return render(
		<ChatImageSourceProvider sessionId="s-1" assetBaseUrl={BASE} remoteHost={remoteHost}>
			<ChatLocalImageStrip text={text} />
		</ChatImageSourceProvider>,
	);
}

describe("ChatLocalImageStrip", () => {
	afterEach(() => vi.restoreAllMocks());

	it("loads each image from the route main names and nothing for a path it maps nowhere", async () => {
		renderStrip(`Saved \`${ARTIFACT}\`, ${WORKSPACE} and \`${ELSEWHERE}\``);
		expect(tallpineBridge.localPath.inspect).toHaveBeenCalledWith(ARTIFACT, "s-1");
		await waitFor(() => expect(screen.getAllByRole("img", { hidden: true })).toHaveLength(2));
		const images = within(screen.getByRole("group", { name: "Images", hidden: true })).getAllByRole("img", { hidden: true });
		expect(images.map((image) => image.getAttribute("src"))).toEqual([
			`${BASE}/api/v1/sessions/ranker-6/artifact-files/a%20light.png`,
			expect.stringMatching(new RegExp(`^${BASE}/api/v1/sessions/s-1/workspace/file/blob\\?path=docs%2Fshot\\.webp&side=after&v=\\d+$`)),
		]);
		expect(images.every((image) => image.getAttribute("loading") === "lazy")).toBe(true);
	});

	it("stays hidden until it loads and disappears when the file cannot be read", async () => {
		renderStrip(`\`${ARTIFACT}\` and \`${WORKSPACE}\``);
		await waitFor(() => expect(screen.getAllByRole("img", { hidden: true })).toHaveLength(2));
		const [first, second] = screen.getAllByRole("img", { hidden: true });
		expect(first!.closest("button")).toHaveClass("invisible");
		fireEvent.load(first!);
		expect(first!.closest("button")).not.toHaveClass("invisible");
		fireEvent.error(second!);
		expect(screen.getAllByRole("img", { hidden: true })).toEqual([first]);
	});

	it("opens the image viewer on click and offers the path menu on right-click", async () => {
		vi.spyOn(navigator, "userAgent", "get").mockReturnValue("Macintosh");
		const perform = vi.spyOn(tallpineBridge.localPath, "perform").mockResolvedValue({ ok: true, performed: "open" });
		renderStrip(`\`${ARTIFACT}\``);
		fireEvent.load(await screen.findByRole("img", { hidden: true }));
		const thumbnail = screen.getByRole("button", { name: "Open image: a light.png" });

		await userEvent.click(thumbnail);
		const dialog = await screen.findByRole("dialog");
		expect(within(dialog).getByRole("img")).toHaveAttribute("src", `${BASE}/api/v1/sessions/ranker-6/artifact-files/a%20light.png`);
		await userEvent.keyboard("{Escape}");

		fireEvent.contextMenu(thumbnail);
		const open = await screen.findByRole("menuitem", { name: "Open" });
		await waitFor(() => expect(open).not.toHaveAttribute("data-disabled"));
		expect(screen.getByRole("menuitem", { name: "Reveal in Finder" })).toBeInTheDocument();
		await userEvent.click(open);
		expect(perform).toHaveBeenCalledWith(ARTIFACT, "open");
	});

	it("shows nothing for a remote-host chat, whose files this daemon cannot serve", () => {
		renderStrip(`\`${ARTIFACT}\``, true);
		expect(screen.queryAllByRole("img", { hidden: true })).toHaveLength(0);
		expect(tallpineBridge.localPath.inspect).not.toHaveBeenCalled();
	});

	it("is added under a finished assistant reply, not one still streaming", async () => {
		mockMain();
		const message = { id: "m1", role: "assistant", text: `Saved \`${ARTIFACT}\``, createdAt: "2026-11-05T09:00:00Z", streaming: true } as ConversationMessage;
		const { rerender } = render(<ChatImageSourceProvider sessionId="s-1" assetBaseUrl={BASE}><AssistantMessage message={message} /></ChatImageSourceProvider>);
		expect(screen.queryByRole("group", { name: "Images", hidden: true })).toBeNull();
		rerender(<ChatImageSourceProvider sessionId="s-1" assetBaseUrl={BASE}><AssistantMessage message={{ ...message, streaming: false }} /></ChatImageSourceProvider>);
		expect(await screen.findAllByRole("img", { hidden: true })).toHaveLength(1);
	});
});
