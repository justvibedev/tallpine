import { afterEach, describe, expect, it } from "vitest";
import { installChromeObject } from "./browser-chrome-object";

type Host = { chrome?: Record<string, unknown> };
const host = globalThis as unknown as Host & { window?: unknown };

afterEach(() => {
	delete host.chrome;
	delete host.window;
});

describe("installChromeObject", () => {
	it("fills Electron's empty window.chrome with the members Chrome has", () => {
		host.window = globalThis;
		host.chrome = {};
		installChromeObject();
		const chrome = host.chrome as Record<string, unknown>;
		expect(Object.keys(chrome)).toEqual(["loadTimes", "csi", "app"]);
		const app = chrome.app as { isInstalled: boolean; getIsInstalled(): boolean; InstallState: Record<string, string> };
		expect(app.isInstalled).toBe(false);
		expect(app.getIsInstalled()).toBe(false);
		expect(app.InstallState.NOT_INSTALLED).toBe("not_installed");
		const loadTimes = (chrome.loadTimes as () => Record<string, unknown>)();
		expect(typeof loadTimes.requestTime).toBe("number");
		expect(typeof loadTimes.connectionInfo).toBe("string");
		const csi = (chrome.csi as () => Record<string, unknown>)();
		expect(typeof csi.startE).toBe("number");
		expect(typeof csi.pageT).toBe("number");
	});

	it("creates window.chrome when the page has none and keeps members it already has", () => {
		host.window = globalThis;
		installChromeObject();
		expect(Object.keys(host.chrome ?? {})).toEqual(["loadTimes", "csi", "app"]);
		const ownApp = { isInstalled: true };
		host.chrome = { app: ownApp };
		installChromeObject();
		expect(host.chrome.app).toBe(ownApp);
	});

	it("is self-contained so executeInMainWorld can serialize it", () => {
		// executeInMainWorld rebuilds the function from its source text.
		const rebuilt = new Function(`return (${installChromeObject.toString()})`)() as () => void;
		host.window = globalThis;
		host.chrome = {};
		rebuilt();
		expect(Object.keys(host.chrome)).toEqual(["loadTimes", "csi", "app"]);
	});
});
