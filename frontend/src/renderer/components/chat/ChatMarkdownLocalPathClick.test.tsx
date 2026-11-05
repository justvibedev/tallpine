import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { tallpineBridge } from "../../lib/bridge";
import { ChatLinkProvider, ChatMarkdown } from "./ChatMarkdown";

/** A plain click on a local path in chat: an existing file opens in its default app, anything else keeps its old action. */
describe("ChatMarkdown local path clicks", () => {
	afterEach(() => vi.restoreAllMocks());

	it("makes prose and inline-code paths clickable but leaves fences and URLs alone", async () => {
		const perform = vi.spyOn(tallpineBridge.localPath, "perform");
		render(<ChatMarkdown text={"See /Users/me/a.txt and `~/b/c.md` or https://x.dev/a/b\n\n```\n/etc/hosts\n```"} />);
		fireEvent.click(screen.getByText("/Users/me/a.txt"), { metaKey: true });
		expect(perform).toHaveBeenCalledWith("/Users/me/a.txt", "reveal");
		fireEvent.click(screen.getByText("~/b/c.md"), { metaKey: true });
		expect(perform).toHaveBeenCalledWith("~/b/c.md", "reveal");
		expect(screen.queryAllByRole("button", { name: /etc\/hosts/ })).toHaveLength(0);
	});

	it("opens a local file on plain click wherever chat names it: bare, inline code, link, workspace file", async () => {
		const write = vi.spyOn(tallpineBridge.clipboard, "writeText").mockResolvedValue(undefined);
		vi.spyOn(tallpineBridge.localPath, "inspect").mockImplementation(async (path) => ({ ok: true, path, kind: "file", canOpen: true }));
		const perform = vi.spyOn(tallpineBridge.localPath, "perform").mockClear().mockResolvedValue({ ok: true, performed: "open" });
		write.mockClear();
		const onFileOpen = vi.fn();
		const onLinkOpen = vi.fn();
		render(
			<ChatLinkProvider onFileOpen={onFileOpen} onLinkOpen={onLinkOpen} workspacePaths={["docs/shot.png"]}>
				<ChatMarkdown text={"See /Users/me/a.txt, `/Users/me/b.md`, [the report](/Users/me/final%20report.pdf), `/w/s-1/docs/shot.png` and [site](https://x.dev/a)"} />
			</ChatLinkProvider>,
		);
		for (const [label, path] of [
			["/Users/me/a.txt", "/Users/me/a.txt"],
			["/Users/me/b.md", "/Users/me/b.md"],
			["the report", "/Users/me/final report.pdf"],
			["/w/s-1/docs/shot.png", "/w/s-1/docs/shot.png"],
		]) {
			fireEvent.click(screen.getByText(label!));
			await waitFor(() => expect(perform).toHaveBeenLastCalledWith(path, "open"));
		}
		expect(onFileOpen).not.toHaveBeenCalled();
		expect(onLinkOpen).not.toHaveBeenCalled();
		expect(write).not.toHaveBeenCalled();

		// Cmd-click keeps each target's open action; a web link still copies on plain click.
		fireEvent.click(screen.getByText("/w/s-1/docs/shot.png"), { metaKey: true });
		expect(onFileOpen).toHaveBeenCalledExactlyOnceWith("/w/s-1/docs/shot.png");
		fireEvent.click(screen.getByText("site"));
		expect(write).toHaveBeenCalledWith("https://x.dev/a");
		expect(perform).toHaveBeenCalledTimes(4);
	});

	it("copies a directory link on plain click, as before", async () => {
		const write = vi.spyOn(tallpineBridge.clipboard, "writeText").mockResolvedValue(undefined);
		vi.spyOn(tallpineBridge.localPath, "inspect").mockResolvedValue({ ok: true, path: "/Users/me/out", kind: "directory", canOpen: true });
		const perform = vi.spyOn(tallpineBridge.localPath, "perform").mockClear();
		render(<ChatLinkProvider onLinkOpen={vi.fn()}><ChatMarkdown text="[output](/Users/me/out)" /></ChatLinkProvider>);
		fireEvent.click(screen.getByText("output"));
		await waitFor(() => expect(write).toHaveBeenCalledWith("/Users/me/out"));
		expect(perform).not.toHaveBeenCalled();
	});
});
