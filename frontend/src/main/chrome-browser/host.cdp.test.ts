import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, it, vi } from "vitest";
import { devtoolsHint } from "./cdp-actions";
import { setupChromeHost } from "./chrome-test-helpers";
import { CHROME_ACTIONS } from "./host";

type Harness = ReturnType<typeof setupChromeHost>;

/** The daemon's browser actions (backend/internal/service/browser/service.go), the full CLI surface. */
function daemonActions(): string[] {
	const source = readFileSync(fileURLToPath(new URL("../../../../backend/internal/service/browser/service.go", import.meta.url)), "utf8");
	const block = /var actions = map\[string\]struct\{\}\{([\s\S]*?)\n\}/.exec(source)?.[1];
	if (!block) throw new Error("actions map not found in service.go");
	return [...block.matchAll(/"([a-z-]+)":\s*\{\}/g)].map((match) => match[1]!)
		.filter((action) => !ROUTER_ACTIONS.has(action));
}

// Actions the command router runs before any host's execute (browser-runtime-command-router.test.ts).
const ROUTER_ACTIONS = new Set(["handoff"]);

const nativeCall = (action: string, args?: Record<string, unknown>) => (_result: unknown, { calls }: Harness) => {
	expect(calls.at(-1)).toMatchObject({ key: "chrome:s1", action, ...(args ? { args } : {}) });
};

/** One Chrome-mode case per daemon action: arguments and what proves it ran. */
const CASES: Record<string, { args?: Record<string, unknown>; setup?: (harness: Harness) => void; verify: (result: unknown, harness: Harness) => void }> = {
	open: { args: { url: "https://a.example/" }, verify: nativeCall("open", { url: "https://a.example/" }) },
	snapshot: { verify: (result) => expect(result).toMatchObject({ text: expect.stringContaining("Save") }) },
	act: { args: { instruction: "click Save" }, verify: (result) => expect(result).toMatchObject({ outcome: "matched", resolvedRef: "e1" }) },
	click: { args: { ref: "e1" }, verify: nativeCall("click", { ref: "e1" }) },
	dblclick: { args: { ref: "e1" }, verify: nativeCall("dblclick", { ref: "e1" }) },
	focus: { args: { ref: "e1" }, verify: nativeCall("focus", { ref: "e1" }) },
	fill: { args: { ref: "e1", text: "hi" }, verify: nativeCall("fill", { ref: "e1", text: "hi" }) },
	type: { args: { ref: "e1", text: "hi" }, verify: nativeCall("type", { ref: "e1", text: "hi" }) },
	press: { args: { key: "Enter" }, verify: nativeCall("press", { key: "Enter" }) },
	hover: { args: { ref: "e1" }, verify: nativeCall("hover", { ref: "e1" }) },
	highlight: { args: { ref: "e1" }, verify: nativeCall("highlight", { ref: "e1" }) },
	unhighlight: { verify: (_result, { connection }) => expect(connection().methods()).toContain("Overlay.hideHighlight") },
	scrollintoview: { args: { ref: "e1" }, verify: nativeCall("scrollintoview", { ref: "e1" }) },
	drag: { args: { ref: "e1", targetRef: "e2" }, verify: nativeCall("drag", { ref: "e1", targetRef: "e2" }) },
	tabs: { verify: (result) => expect(result).toMatchObject({ activeTabId: "T1", tabs: [{ id: "T1" }] }) },
	"tab-new": { verify: (result) => expect(result).toMatchObject({ id: "T2", active: true }) },
	"tab-select": { args: { tabId: "T1" }, verify: (result) => expect(result).toMatchObject({ id: "T1", active: true }) },
	"tab-close": { args: { tabId: "T1" }, verify: (result) => expect(result).toMatchObject({ closedTabId: "T1" }) },
	scroll: { args: { direction: "down" }, verify: nativeCall("scroll", { direction: "down", amount: 600 }) },
	select: { args: { ref: "e1", value: "a" }, verify: nativeCall("select", { ref: "e1", value: "a" }) },
	check: { args: { ref: "e1" }, verify: nativeCall("check", { ref: "e1" }) },
	uncheck: { args: { ref: "e1" }, verify: nativeCall("uncheck", { ref: "e1" }) },
	get: { args: { property: "title" }, verify: nativeCall("get", { property: "title" }) },
	wait: { args: { ms: 10 }, verify: nativeCall("wait", { ms: 10 }) },
	screenshot: { verify: (result) => expect(result).toMatchObject({ data: "iVBOR", target: { tabId: "T1" } }) },
	"network-start": { args: { durationSeconds: 5 }, verify: (result) => expect(result).toMatchObject({ active: true, tabId: "T1" }) },
	"network-status": { verify: (result) => expect(result).toMatchObject({ active: false, metadataOnly: true }) },
	"network-list": { verify: (result) => expect(result).toMatchObject({ requests: [] }) },
	"network-stop": { verify: (result) => expect(result).toMatchObject({ active: false }) },
	"network-clear": { verify: (result) => expect(result).toMatchObject({ requestCount: 0 }) },
	console: { verify: nativeCall("console") },
	errors: { verify: nativeCall("errors") },
	frame: { args: { selector: "#child" }, verify: nativeCall("frame", { selector: "#child" }) },
	dialog: { args: { response: "accept" }, verify: nativeCall("dialog", { response: "accept" }) },
	"devtools-open": { verify: (result) => expect(result).toMatchObject({ open: false, activeTabId: "T1", hint: expect.stringContaining("open them") }) },
	"devtools-close": { verify: (result) => expect(result).toMatchObject({ open: false, hint: expect.stringContaining("close them") }) },
	viewport: { args: { width: 390, height: 844 }, verify: (result) => expect(result).toMatchObject({ width: 390, height: 844, target: { tabId: "T1" } }) },
	back: { setup: history(1), verify: (result) => expect(result).toMatchObject({ url: "https://a.example/one", target: { tabId: "T1" } }) },
	forward: { setup: history(0), verify: (result) => expect(result).toMatchObject({ url: "https://a.example/two", target: { tabId: "T1" } }) },
	reload: { setup: history(0), verify: (_result, { connection }) => expect(connection().methods()).toContain("Page.reload") },
	eval: {
		args: { expression: "1 + 1" },
		setup: ({ connection }) => {
			connection().handlers["Runtime.evaluate"] = () => ({ result: { type: "number", value: 2 } });
		},
		verify: (result) => expect(result).toMatchObject({ type: "number", json: "2", target: { tabId: "T1" } }),
	},
	upload: {
		args: { ref: "e1", paths: ["/work/a.txt"] },
		setup: ({ runtime, connection }) => {
			runtime.runAction.mockImplementationOnce(async () => {
				connection().emit({ method: "Page.fileChooserOpened", params: { mode: "selectSingle", backendNodeId: 4 }, sessionId: "S-T1" });
				return {};
			});
		},
		verify: (result) => expect(result).toMatchObject({ uploaded: 1, files: ["a.txt"] }),
	},
};

/** A two-page history at index start, for back/forward/reload. */
function history(start: number) {
	return ({ connection }: Harness) => {
		const entries = [
			{ id: 1, url: "https://a.example/one", title: "One" },
			{ id: 2, url: "https://a.example/two", title: "Two" },
		];
		let index = start;
		const commit = () => connection().emit({ method: "Page.frameNavigated", params: { frame: { id: "main" } }, sessionId: "S-T1" });
		connection().handlers["Page.getNavigationHistory"] = () => ({ currentIndex: index, entries });
		connection().handlers["Page.navigateToHistoryEntry"] = (params) => {
			index = entries.findIndex((entry) => entry.id === params.entryId);
			commit();
			return {};
		};
		connection().handlers["Page.reload"] = () => {
			commit();
			return {};
		};
	};
}

afterEach(() => {
	vi.useRealTimers();
});

describe("Chrome mode command surface", () => {
	const actions = daemonActions();

	it("has a Chrome-host case and support for every daemon browser action", () => {
		expect(actions.length).toBeGreaterThan(30);
		expect(actions.filter((action) => !(action in CASES))).toEqual([]);
		expect(actions.filter((action) => !CHROME_ACTIONS.has(action))).toEqual([]);
	});

	it.each(actions)("runs %s in Chrome mode", async (action) => {
		const harness = setupChromeHost();
		// The session has a page with a snapshot (act's matcher reads it) before each command.
		await harness.host.execute("s1", "snapshot");
		const testCase = CASES[action]!;
		testCase.setup?.(harness);
		const result = await harness.host.execute("s1", action, testCase.args ?? {});
		testCase.verify(result, harness);
	});
});

describe("Chrome mode CDP-direct commands", () => {
	const pageEvent = (harness: Harness, method: string, params: Record<string, unknown>, sessionId = "S-T1") =>
		harness.connection().emit({ method, params, sessionId });
	const request = (requestId: string) => ({ requestId, request: { method: "GET", url: `https://a.example/${requestId}` } });

	it("records only the captured tab's own page session, and keeps it across tab switches", async () => {
		const harness = setupChromeHost();
		const { host } = harness;
		await host.execute("s1", "open", { url: "https://a.example/" });
		await host.execute("s1", "network-start", { durationSeconds: 30 });
		expect(harness.connection().sent).toContainEqual({ method: "Network.enable", params: {}, sessionId: "S-T1" });
		pageEvent(harness, "Network.requestWillBeSent", request("own"));
		// An out-of-process frame the native runtime attached under the page session.
		pageEvent(harness, "Target.attachedToTarget", { sessionId: "S-child", targetInfo: { targetId: "F1", type: "iframe" } });
		pageEvent(harness, "Network.requestWillBeSent", request("child-frame"), "S-child");
		pageEvent(harness, "Network.requestWillBeSent", request("browser-level"), "");
		await host.execute("s1", "tab-new", { url: "https://b.example/" });
		pageEvent(harness, "Network.requestWillBeSent", request("own-2"));
		const listed = (await host.execute("s1", "network-list")) as { tabId: string; requests: { url: string }[] };
		expect(listed.tabId).toBe("T1");
		expect(listed.requests.map((entry) => entry.url)).toEqual(["https://a.example/own", "https://a.example/own-2"]);
		expect(await host.execute("s1", "network-start", { durationSeconds: 30 })).toMatchObject({ alreadyActive: true, tabId: "T1" });
	});

	it("ends a capture when its tab closes, the session ends, or Chrome quits", async () => {
		vi.useFakeTimers();
		const harness = setupChromeHost();
		const { host } = harness;
		await host.execute("s1", "network-start", { durationSeconds: 30 });
		await host.execute("s1", "tab-close", { tabId: "T1" });
		expect(vi.getTimerCount()).toBe(0);
		pageEvent(harness, "Network.requestWillBeSent", request("late"));
		expect(await host.execute("s1", "network-status")).toMatchObject({ active: false, requestCount: 0 });

		await host.execute("s1", "network-start", { durationSeconds: 30 });
		expect(vi.getTimerCount()).toBe(1);
		await host.destroySession("s1");
		expect(vi.getTimerCount()).toBe(0);

		await host.execute("s2", "network-start", { durationSeconds: 30 });
		expect(vi.getTimerCount()).toBe(1);
		harness.quit();
		await host.execute("s2", "network-status");
		expect(vi.getTimerCount()).toBe(0);
	});

	it("console and errors select the session's tab in the native runtime first", async () => {
		const harness = setupChromeHost((action) => (action === "errors" ? { messages: ["boom"] } : { messages: [] }));
		const errors = await harness.host.execute("s1", "errors");
		expect(harness.calls.map((call) => call.action)).toEqual(["tab-select", "errors"]);
		expect(errors).toMatchObject({ messages: [{ level: "error", message: expect.stringContaining("boom") }], untrustedExternalContent: true });
	});

	it("viewport emulates on the session's tab only", async () => {
		const harness = setupChromeHost();
		await harness.host.execute("s1", "open", { url: "https://a.example/" });
		await harness.host.execute("s2", "open", { url: "https://b.example/" });
		await harness.host.execute("s2", "viewport", { width: 800, height: 600 });
		const emulated = harness.connection().sent.filter((entry) => entry.method === "Emulation.setDeviceMetricsOverride");
		expect(emulated).toEqual([{ method: "Emulation.setDeviceMetricsOverride", params: { width: 800, height: 600, deviceScaleFactor: 0, mobile: false }, sessionId: "S-T2" }]);
	});

	it("devtools-open brings the tab to the front and hands back the keystroke", async () => {
		const harness = setupChromeHost();
		await harness.host.execute("s1", "open", { url: "https://a.example/" });
		const result = await harness.host.execute("s1", "devtools-open");
		expect(harness.connection().sent.at(-1)).toEqual({ method: "Target.activateTarget", params: { targetId: "T1" } });
		expect(result).toMatchObject({ viewId: "chrome-s1", open: false, activeTabId: "T1", target: { tabId: "T1" } });
		expect(devtoolsHint(true, "darwin")).toContain("Cmd+Opt+I");
		expect(devtoolsHint(false, "linux")).toContain("Ctrl+Shift+I");
	});
});
