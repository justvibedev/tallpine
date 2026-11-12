import { fireEvent, render, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { tallpineBridge } from "../../lib/bridge";
import { ChatLocalMedia } from "./ChatLocalMedia";
import { ChatImageSourceProvider } from "./chat-image-source";
import type { LocalImageSource } from "../../../shared/local-path";

const BASE = "http://127.0.0.1:3101";
const VIDEO = "/Users/me/.tallpine/data/artifacts/ranker-6/demo run.mp4";
const AUDIO = "/Users/me/.tallpine/data/worktrees/proj/s-1/out/take.mp3";
const ELSEWHERE = "/Users/me/Desktop/private.mov";

const SOURCES: Record<string, LocalImageSource | undefined> = {
	[VIDEO]: { kind: "artifact", sessionId: "ranker-6", path: "demo run.mp4" },
	[AUDIO]: { kind: "workspace", path: "out/take.mp3" },
	[ELSEWHERE]: undefined,
};

function renderMedia(text: string, remoteHost = false) {
	vi.spyOn(tallpineBridge.localPath, "inspect").mockImplementation(async (path) => ({ ok: true, path, kind: "file", canOpen: true, image: SOURCES[path] }));
	return render(
		<ChatImageSourceProvider sessionId="s-1" assetBaseUrl={BASE} remoteHost={remoteHost}>
			<ChatLocalMedia text={text} />
		</ChatImageSourceProvider>,
	);
}

describe("ChatLocalMedia", () => {
	afterEach(() => vi.restoreAllMocks());

	it("plays local video and audio inline from the streamed routes main names", async () => {
		const { container } = renderMedia(`Recorded \`${VIDEO}\`, audio ${AUDIO} and \`${ELSEWHERE}\``);
		await waitFor(() => expect(container.querySelector("video")).not.toBeNull());
		const video = container.querySelector("video")!;
		const audio = container.querySelector("audio")!;
		expect(video.hasAttribute("controls")).toBe(true);
		expect(video.getAttribute("src")).toBe(`${BASE}/api/v1/sessions/ranker-6/artifact-files/demo%20run.mp4`);
		expect(video.className).toContain("max-w-[760px]");
		expect(video.className).toContain("max-h-[60vh]");
		expect(audio.getAttribute("src")).toMatch(new RegExp(`^${BASE}/api/v1/sessions/s-1/preview/files/out/take\\.mp3\\?raw=true&v=\\d+$`));
		expect(container.querySelectorAll("video, audio")).toHaveLength(2);
	});

	it("drops a player whose file cannot be read", async () => {
		const { container } = renderMedia(`\`${VIDEO}\``);
		await waitFor(() => expect(container.querySelector("video")).not.toBeNull());
		fireEvent.error(container.querySelector("video")!);
		expect(container.querySelector("video")).toBeNull();
	});

	it("renders nothing and never inspects on a remote host", () => {
		const { container } = renderMedia(`\`${VIDEO}\` and ${AUDIO}`, true);
		expect(container.querySelector("video, audio")).toBeNull();
		expect(tallpineBridge.localPath.inspect).not.toHaveBeenCalled();
	});

	it("renders nothing when the text names no media", () => {
		const { container } = renderMedia("No files here, see /Users/me/a.png");
		expect(container).toBeEmptyDOMElement();
	});
});
