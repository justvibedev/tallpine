import { afterEach, describe, expect, it } from "vitest";
import {
	measuredRenderHeight,
	readRenderContentHeight,
	readRenderLinkRequest,
	readRenderTheme,
	renderFileName,
	renderThemeFragment,
	renderThemeMessage,
} from "./render-frame";

describe("render-frame helpers", () => {
	afterEach(() => {
		document.documentElement.removeAttribute("data-theme");
		document.documentElement.style.cssText = "";
	});

	it("reads the taller of the nearest measured heights on each side of a width", () => {
		const heights: Array<[number, number]> = [
			[320, 900],
			[520, 400],
			[860, 500],
		];
		expect(measuredRenderHeight(heights, 520)).toBe(400);
		expect(measuredRenderHeight(heights, 700)).toBe(500);
		expect(measuredRenderHeight(heights, 400)).toBe(900);
		expect(measuredRenderHeight(heights, 240)).toBe(900);
		expect(measuredRenderHeight(heights, 1200)).toBe(500);
		expect(measuredRenderHeight([[640, 300]], 1200)).toBe(300);
	});

	it("names a saved page after its title, without characters a file system refuses", () => {
		expect(renderFileName("Turns by day")).toBe("Turns by day.html");
		expect(renderFileName('Q3: a/b\\c *?"<>| \u0007 report\n')).toBe("Q3 a b c report.html");
		expect(renderFileName("")).toBe("Page.html");
		expect(renderFileName(' /:*?"<>| ')).toBe("Page.html");
		expect(renderFileName(`${"x".repeat(119)} tail`)).toBe(`${"x".repeat(119)}.html`);
		expect(renderFileName("y".repeat(300))).toBe(`${"y".repeat(120)}.html`);
	});

	it("reads only the bootstrap's own protocol messages", () => {
		expect(readRenderContentHeight({ jsonrpc: "2.0", method: "ui/notifications/size-changed", params: { height: 412.4 } })).toBe(412.4);
		expect(readRenderContentHeight({ method: "ui/notifications/size-changed", params: { height: 412 } })).toBeUndefined();
		expect(readRenderLinkRequest({ jsonrpc: "2.0", id: 1, method: "ui/open-link", params: { url: "https://x.dev/a" } })).toBe("https://x.dev/a");
		expect(readRenderLinkRequest({ jsonrpc: "2.0", id: 1, method: "ui/open-link", params: { url: "javascript:alert(1)" } })).toBeUndefined();
	});

	it("maps Tallpine tokens to the agent-facing variables and follows data-theme", () => {
		document.documentElement.style.setProperty("--color-bg-primary", "rgb(1, 2, 3)");
		document.documentElement.setAttribute("data-theme", "light");
		const theme = readRenderTheme();
		expect(theme.appearance).toBe("light");
		expect(theme.variables["--background"]).toBe("rgb(1, 2, 3)");
		expect(theme.variables["--chart-6"]).toBeDefined();
	});

	it("hands the page its display mode in the fragment and in every context message", () => {
		const theme = { appearance: "light" as const, variables: { "--background": "#fff" } };
		const fragment = renderThemeFragment(theme, "fullscreen");
		expect(fragment.startsWith("#ao-theme=")).toBe(true);
		expect(JSON.parse(decodeURIComponent(fragment.slice("#ao-theme=".length)))).toEqual({
			appearance: "light",
			variables: { "--background": "#fff" },
			displayMode: "fullscreen",
			wrapContent: false,
		});
		expect(renderThemeMessage(theme, "inline")).toEqual({
			jsonrpc: "2.0",
			method: "ui/notifications/host-context-changed",
			params: { theme: "light", styles: { variables: { "--background": "#fff" } }, displayMode: "inline", wrapContent: false },
		});
	});

	it("carries the wrapping choice in initial and live context for renders and artifacts", () => {
		const theme = { appearance: "dark" as const, variables: {} };
		for (const scrollable of [false, true]) {
			const fragment = renderThemeFragment(theme, "inline", scrollable, true);
			expect(JSON.parse(decodeURIComponent(fragment.slice("#ao-theme=".length)))).toMatchObject({ wrapContent: true });
			expect(renderThemeMessage(theme, "fullscreen", scrollable, true).params.wrapContent).toBe(true);
			expect(renderThemeMessage(theme, "inline", scrollable, false).params.wrapContent).toBe(false);
		}
	});
});
