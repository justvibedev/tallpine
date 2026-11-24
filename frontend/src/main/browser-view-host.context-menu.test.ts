import { describe, expect, it, vi } from "vitest";
import type { MenuItemConstructorOptions } from "electron";
import { setupHost } from "./browser-view-host-test-helpers";
import { pageMenuTemplate, type PageMenuActions, type PageMenuParams } from "./browser-view-host/context-menu";
import { trayText } from "./tray-text";

vi.mock("electron", async (importOriginal) => {
	const actual = await importOriginal<typeof import("electron")>();
	return { ...actual, net: { fetch: vi.fn() } };
});

const PAGE = "https://docs.example/guide";
const EDIT_NONE = { canUndo: false, canRedo: false, canCut: false, canCopy: false, canPaste: false, canDelete: false, canSelectAll: false, canEditRichly: false };

function params(extra: Partial<PageMenuParams> = {}): PageMenuParams {
	return {
		x: 10, y: 20, linkURL: "", srcURL: "", mediaType: "none", selectionText: "",
		isEditable: false, misspelledWord: "", dictionarySuggestions: [], editFlags: EDIT_NONE,
		...extra,
	};
}

function fakeActions(): PageMenuActions & Record<string, ReturnType<typeof vi.fn>> {
	return {
		openInNewTab: vi.fn(), openInSystemBrowser: vi.fn(), copyText: vi.fn(), copyImageAt: vi.fn(), saveURL: vi.fn(),
		replaceMisspelling: vi.fn(), addToDictionary: vi.fn(), edit: vi.fn(), nav: vi.fn(), inspect: vi.fn(),
		canGoBack: vi.fn(() => true), canGoForward: vi.fn(() => false),
	};
}

const labels = (items: MenuItemConstructorOptions[]) => items.map((item) => (item.type === "separator" ? "-" : item.label));
const click = (items: MenuItemConstructorOptions[], label: string) =>
	(items.find((item) => item.label === label)!.click as () => void)();

const en = trayText("en");

describe("page right-click menu", () => {
	it("offers a link's new tab, default browser and copy, then Inspect", () => {
		const actions = fakeActions();
		const items = pageMenuTemplate(params({ linkURL: "https://a.example/x" }), actions, en);
		expect(labels(items)).toEqual(["Open Link in New Tab", "Open Link in Default Browser", "Copy Link Address", "-", "Inspect"]);
		click(items, "Open Link in New Tab");
		click(items, "Open Link in Default Browser");
		click(items, "Copy Link Address");
		click(items, "Inspect");
		expect(actions.openInNewTab).toHaveBeenCalledWith("https://a.example/x");
		expect(actions.openInSystemBrowser).toHaveBeenCalledWith("https://a.example/x");
		expect(actions.copyText).toHaveBeenCalledWith("https://a.example/x");
		expect(actions.inspect).toHaveBeenCalledWith(10, 20);
	});

	it("only copies a link that is not a web address", () => {
		const items = pageMenuTemplate(params({ linkURL: "javascript:void(0)" }), fakeActions(), en);
		expect(labels(items)).toEqual(["Copy Link Address", "-", "Inspect"]);
	});

	it("saves an image through the download manager and copies it at the click point", () => {
		const actions = fakeActions();
		const src = "https://cdn.example/cat.png";
		const items = pageMenuTemplate(params({ mediaType: "image", srcURL: src }), actions, en);
		expect(labels(items)).toEqual(["Open Image in New Tab", "Save Image", "Copy Image", "Copy Image Address", "-", "Inspect"]);
		click(items, "Save Image");
		click(items, "Copy Image");
		click(items, "Copy Image Address");
		expect(actions.saveURL).toHaveBeenCalledWith(src);
		expect(actions.copyImageAt).toHaveBeenCalledWith(10, 20);
		expect(actions.copyText).toHaveBeenCalledWith(src);
	});

	it("copies or searches a selection in a new tab", () => {
		const actions = fakeActions();
		const items = pageMenuTemplate(params({ selectionText: "  tall   pine trees  " }), actions, en);
		expect(labels(items)).toEqual(["Copy", "Search Google for “tall pine trees”", "-", "Inspect"]);
		click(items, "Copy");
		click(items, "Search Google for “tall pine trees”");
		expect(actions.edit).toHaveBeenCalledWith("copy");
		expect(actions.openInNewTab).toHaveBeenCalledWith("https://www.google.com/search?q=tall%20pine%20trees");
	});

	it("puts dictionary suggestions first for a misspelled word in a field", () => {
		const actions = fakeActions();
		const items = pageMenuTemplate(
			params({
				isEditable: true, misspelledWord: "tallpien", dictionarySuggestions: ["tallpine", "tall pine"],
				editFlags: { ...EDIT_NONE, canCut: true, canCopy: true, canPaste: true, canSelectAll: true },
			}),
			actions,
			en,
		);
		expect(labels(items)).toEqual([
			"tallpine", "tall pine", "Add to Dictionary", "-", "Undo", "Redo", "-", "Cut", "Copy", "Paste", "Select All", "-", "Inspect",
		]);
		expect(items.find((item) => item.label === "Undo")?.enabled).toBe(false);
		expect(items.find((item) => item.label === "Paste")?.enabled).toBe(true);
		click(items, "tallpine");
		click(items, "Add to Dictionary");
		click(items, "Paste");
		expect(actions.replaceMisspelling).toHaveBeenCalledWith("tallpine");
		expect(actions.addToDictionary).toHaveBeenCalledWith("tallpien");
		expect(actions.edit).toHaveBeenCalledWith("paste");
	});

	it("says when a misspelled word has no suggestions", () => {
		const items = pageMenuTemplate(params({ isEditable: true, misspelledWord: "zzxq" }), fakeActions(), en);
		expect(items[0]).toMatchObject({ label: "No Guesses Found", enabled: false });
	});

	it("offers Back, Forward and Reload on the bare page", () => {
		const actions = fakeActions();
		const items = pageMenuTemplate(params(), actions, en);
		expect(labels(items)).toEqual(["Back", "Forward", "Reload", "-", "Inspect"]);
		expect(items.map((item) => item.enabled)).toEqual([true, false, undefined, undefined, undefined]);
		click(items, "Reload");
		expect(actions.nav).toHaveBeenCalledWith("reload");
	});

	it("uses the app's language", () => {
		const items = pageMenuTemplate(params({ linkURL: "https://a.example/" }), fakeActions(), trayText("de"));
		expect(items[0].label).toBe("Link in neuem Tab öffnen");
	});
});

function menuHost() {
	const menus: MenuItemConstructorOptions[][] = [];
	const popup = vi.fn();
	const writeText = vi.fn();
	const harness = setupHost(undefined, {
		buildPageMenu: (items) => {
			menus.push(items);
			return { popup };
		},
		clipboard: { writeImage: vi.fn(), writeText },
		getLocale: () => "en",
	});
	const contents = harness.webContents as typeof harness.webContents & Record<string, unknown>;
	contents.inspectElement = vi.fn();
	contents.downloadURL = vi.fn();
	const rightClick = (extra: Partial<PageMenuParams> = {}) =>
		harness.webContentsListeners.get("context-menu")?.({} as never, params(extra) as never);
	return { ...harness, menus, popup, writeText, contents, rightClick };
}

describe("browser tab right-click", () => {
	it("opens the native menu on a person's tab and acts on that tab", async () => {
		const { invoke, webContents, menus, popup, writeText, contents, rightClick, openDevTools } = menuHost();
		await invoke("browser:ensure", "sess-1");
		await webContents.loadURL(PAGE);

		rightClick({ linkURL: "https://a.example/next" });
		expect(popup).toHaveBeenCalledTimes(1);
		click(menus[0], "Copy Link Address");
		expect(writeText).toHaveBeenCalledWith("https://a.example/next");

		rightClick({ mediaType: "image", srcURL: "https://cdn.example/cat.png" });
		click(menus[1], "Save Image");
		expect(contents.downloadURL).toHaveBeenCalledWith("https://cdn.example/cat.png");

		rightClick({ x: 33, y: 44 });
		click(menus[2], "Inspect");
		await vi.waitFor(() => expect(contents.inspectElement).toHaveBeenCalledWith(33, 44));
		expect(openDevTools).toHaveBeenCalled();
	});

	it("never opens a menu on an agent tab", async () => {
		const { host, webContents, popup, rightClick } = menuHost();
		// The agent opens the session, so its first tab is agent-driven.
		await host.execute("sess-1", "tabs");
		await webContents.loadURL(PAGE);
		rightClick({ linkURL: "https://a.example/next" });
		rightClick();
		expect(popup).not.toHaveBeenCalled();
	});
});
