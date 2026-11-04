import { describe, expect, it, vi } from "vitest";
import type { TallpineBridge } from "../../preload";

const exposed = vi.hoisted(() => ({ api: undefined as unknown }));

vi.mock("electron", () => ({
	contextBridge: {
		exposeInMainWorld: (_key: string, api: unknown) => {
			exposed.api = api;
		},
	},
	ipcRenderer: { invoke: vi.fn(), off: vi.fn(), on: vi.fn(), send: vi.fn() },
	webUtils: { getPathForFile: vi.fn() },
}));

/** Dotted paths of every function/leaf the bridge exposes. */
function bridgePaths(value: unknown, prefix = ""): string[] {
	if (value === null || typeof value !== "object") return [prefix];
	const out: string[] = [];
	for (const [key, child] of Object.entries(value)) {
		const path = prefix ? `${prefix}.${key}` : key;
		out.push(...(typeof child === "object" && child !== null ? bridgePaths(child, path) : [path]));
	}
	return out;
}

// setup.ts leaves daemonSocket undefined on purpose: tests stub WebSocket and
// daemon-websocket.test.ts passes its own bridge.
const INTENTIONALLY_ABSENT = /^daemonSocket\./;

describe("test bridge", () => {
	it("has every key of the real preload bridge", async () => {
		await import("../../preload");
		const real = bridgePaths(exposed.api as TallpineBridge);
		expect(real.length).toBeGreaterThan(50);
		// The shared test setup installs window.tallpine before each test.
		const mock = new Set(bridgePaths(window.tallpine));
		expect(real.filter((path) => !INTENTIONALLY_ABSENT.test(path) && !mock.has(path))).toEqual([]);
	});
});
