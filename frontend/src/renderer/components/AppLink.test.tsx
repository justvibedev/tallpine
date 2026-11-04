import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AppBrowserLinkContext, AppLink } from "./AppLink";
import { ProductExternalLink } from "./ProductExternalLink";
import { tallpineBridge } from "../lib/bridge";
import { useUiStore } from "../stores/ui-store";

const url = "https://github.com/justvibedev/tallpine-dev/pull/42";
afterEach(() => vi.restoreAllMocks());

describe("AppLink", () => {
	it("opens PR links in ao even when the product link stops propagation", () => {
		const open = vi.fn();
		const external = vi.spyOn(tallpineBridge.app, "openExternal").mockResolvedValue(undefined);
		render(<AppBrowserLinkContext.Provider value={open}>
			<ProductExternalLink href={url} stopPropagation>PR #42</ProductExternalLink>
		</AppBrowserLinkContext.Provider>);
		fireEvent.click(screen.getByRole("link"));
		expect(open).toHaveBeenCalledExactlyOnceWith(url);
		expect(external).not.toHaveBeenCalled();
	});

	it("offers ao, external, and copy actions without opening on right-click", async () => {
		const user = userEvent.setup();
		const open = vi.fn();
		render(<AppBrowserLinkContext.Provider value={open}><AppLink href={url}>PR</AppLink></AppBrowserLinkContext.Provider>);
		fireEvent.contextMenu(screen.getByRole("link"));
		expect(open).not.toHaveBeenCalled();
		expect(screen.getAllByRole("menuitem").map(item => item.textContent)).toEqual([
			"Open in Tallpine Browser", "Open in external browser", "Copy link",
		]);
		await user.click(screen.getByRole("menuitem", { name: "Open in Tallpine Browser" }));
		expect(open).toHaveBeenCalledExactlyOnceWith(url);
	});

	it("falls back externally and disables ao when no active browser is available", () => {
		const external = vi.spyOn(tallpineBridge.app, "openExternal").mockResolvedValue(undefined);
		render(<AppLink href={url}>PR</AppLink>);
		fireEvent.click(screen.getByRole("link"));
		expect(external).toHaveBeenCalledExactlyOnceWith(url);
		fireEvent.contextMenu(screen.getByRole("link"));
		expect(screen.getByRole("menuitem", { name: "Open in Tallpine Browser" })).toHaveAttribute("aria-disabled", "true");
	});

	it("preserves handlers that cancel navigation and fragment links", () => {
		const open = vi.fn();
		const handled = vi.fn((event) => event.preventDefault());
		render(<AppBrowserLinkContext.Provider value={open}>
			<AppLink href={url} onClick={handled}>Custom</AppLink>
			<AppLink href="#section">Section</AppLink>
		</AppBrowserLinkContext.Provider>);
		fireEvent.click(screen.getByRole("link", { name: "Custom" }));
		fireEvent.click(screen.getByRole("link", { name: "Section" }));
		expect(handled).toHaveBeenCalledOnce();
		expect(open).not.toHaveBeenCalled();
	});

	it("offers Files for verified workspace paths without offering the system browser", async () => {
		const user = userEvent.setup();
		const open = vi.fn();
		const openFile = vi.fn();
		const path = "reports/final.html";
		render(
			<AppLink
				href="C:\\worktree\\reports\\final.html"
				onBrowserOpen={open}
				inAppLink={() => true}
				filePath={path}
				onFileOpen={openFile}
			>
				Report
			</AppLink>,
		);

		fireEvent.contextMenu(screen.getByRole("link", { name: "Report" }));
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual([
			"Open in Tallpine Browser",
			"Open in Files",
			"Copy link",
		]);
		await user.click(screen.getByRole("menuitem", { name: "Open in Files" }));
		expect(openFile).toHaveBeenCalledExactlyOnceWith(path);
		expect(open).not.toHaveBeenCalled();
	});

	it("mounts a caller supplied preview only after a session link opens", async () => {
		const preview = vi.fn(() => <div data-testid="session-preview">Worker details</div>);
		render(<AppLink href="tallpine://sessions/project/session" hoverPreview={preview}>Worker</AppLink>);

		expect(preview).not.toHaveBeenCalled();
		fireEvent.pointerOver(screen.getByRole("link"), { pointerType: "mouse" });
		await waitFor(() => expect(screen.getByTestId("session-preview")).toBeInTheDocument());
		expect(preview).toHaveBeenCalled();
	});

	it.each([
		["Macintosh", "MacIntel", "Reveal in Finder"],
		["Windows NT", "Win32", "Reveal in File Explorer"],
		["X11; Linux", "Linux x86_64", "Reveal in file manager"],
	])("labels Reveal for the platform (%s)", async (userAgent, platform, label) => {
		vi.spyOn(navigator, "userAgent", "get").mockReturnValue(userAgent);
		vi.spyOn(navigator, "platform", "get").mockReturnValue(platform);
		render(<AppLink href="tallpine://artifact/x" localPath="/a/b/c.html">c</AppLink>);
		fireEvent.contextMenu(screen.getByRole("link"));
		expect(await screen.findByRole("menuitem", { name: label })).toBeInTheDocument();
	});

	it("adds Reveal / Open / Copy path for a local-path link and enables Open only for openable types", async () => {
		const inspect = vi.spyOn(tallpineBridge.localPath, "inspect");
		const perform = vi.spyOn(tallpineBridge.localPath, "perform");
		vi.spyOn(navigator, "userAgent", "get").mockReturnValue("Macintosh");
		inspect.mockResolvedValue({ ok: true, path: "/a/b/gallery.html", kind: "file", canOpen: true });
		const { unmount } = render(<AppLink href="tallpine://artifact/x" localPath="/a/b/gallery.html">gallery</AppLink>);
		fireEvent.contextMenu(screen.getByRole("link"));
		const reveal = await screen.findByRole("menuitem", { name: "Reveal in Finder" });
		await waitFor(() => expect(reveal).not.toHaveAttribute("data-disabled"));
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Reveal in Finder", "Open", "Copy path", "Copy link"]);
		await userEvent.click(screen.getByRole("menuitem", { name: "Open" }));
		expect(perform).toHaveBeenCalledWith("/a/b/gallery.html", "open");
		unmount();

		inspect.mockResolvedValue({ ok: true, path: "/a/run.sh", kind: "file", canOpen: false });
		render(<AppLink href="tallpine://artifact/y" localPath="/a/run.sh">run</AppLink>);
		fireEvent.contextMenu(screen.getByRole("link"));
		await waitFor(() => expect(screen.getByRole("menuitem", { name: "Reveal in Finder" })).not.toHaveAttribute("data-disabled"));
		expect(screen.getByRole("menuitem", { name: "Open" })).toHaveAttribute("data-disabled");
	});

	it("toasts when the file is missing (inspect says not_found)", async () => {
		vi.spyOn(navigator, "userAgent", "get").mockReturnValue("Macintosh");
		vi.spyOn(tallpineBridge.localPath, "inspect").mockResolvedValue({ ok: false, reason: "not_found" });
		vi.spyOn(tallpineBridge.localPath, "perform").mockResolvedValue({ ok: false, reason: "not_found" });
		const show = vi.spyOn(useUiStore.getState(), "showGlobalToast");
		render(<AppLink href="tallpine://artifact/z" localPath="/a/gone.html">gone</AppLink>);
		fireEvent.contextMenu(screen.getByRole("link"));
		const reveal = await screen.findByRole("menuitem", { name: "Reveal in Finder" });
		await waitFor(() => expect(reveal).not.toHaveAttribute("data-disabled"));
		await userEvent.click(reveal);
		await waitFor(() => expect(show).toHaveBeenCalledWith("File not found", undefined, "info"));
	});
});
