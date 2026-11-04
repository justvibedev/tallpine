import os from "node:os";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";

// A callable Proxy that answers every property and call with another proxy, so
// main.ts can evaluate all of its top-level statements without Electron.
const deep = vi.hoisted(() => {
	const setPath = vi.fn();
	const make = (name: string): unknown => {
		const fn = function () {} as unknown as Record<string | symbol, unknown>;
		return new Proxy(fn, {
			get: (_t, prop) => {
				if (prop === Symbol.toPrimitive) return () => name;
				if (prop === "then") return undefined; // not thenable: avoid awaiting forever
				if (prop === "isPackaged") return false;
				if (prop === "getVersion") return () => "0.0.0-test";
				if (prop === "getAppPath") return () => process.cwd();
				if (prop === "requestSingleInstanceLock") return () => true;
				if (prop === "getPath") return () => process.cwd();
				if (prop === "setPath") return setPath;
				return make(`${name}.${String(prop)}`);
			},
			apply: () => {
				// app.whenReady().then(cb) must not run cb: return a pending promise
				if (name.endsWith("whenReady")) return new Promise(() => {});
				return make(`${name}()`);
			},
			construct: () => make(`new ${name}`) as object,
		});
	};
	return { make, setPath };
});

const ELECTRON_NAMES = [
	"app", "BaseWindow", "BrowserWindow", "clipboard", "ClipboardItem", "dialog", "ipcMain", "Menu", "nativeTheme", "net",
	"nativeImage", "Notification", "powerMonitor", "protocol", "shell", "session", "WebContentsView", "webContents",
	"utilityProcess", "Tray", "screen", "safeStorage", "globalShortcut", "contextBridge", "ipcRenderer", "webUtils", "MenuItem",
];

vi.mock("electron", () => Object.fromEntries(ELECTRON_NAMES.map((n) => [n, deep.make(`electron.${n}`)])));

describe("main process entry", () => {
	it("evaluates every top-level statement without throwing", async () => {
		vi.stubGlobal("MAIN_WINDOW_VITE_DEV_SERVER_URL", undefined);
		vi.stubGlobal("MAIN_WINDOW_VITE_NAME", "main_window");
		vi.stubGlobal("__TALLPINE_TEAM_ID__", "");
		vi.stubGlobal("__TALLPINE_BUILD_INFO__", { commit: "test" });
		await expect(import("../main")).resolves.toBeDefined();
	});

	// CLAUDE.md hard rule: Electron userData stays pinned under ~/.tallpine.
	it("pins Electron userData under ~/.tallpine (dev build: ~/.tallpine/dev/electron)", () => {
		const userData = deep.setPath.mock.calls.find(([name]) => name === "userData");
		expect(userData?.[1]).toBe(path.join(os.homedir(), ".tallpine", "dev", "electron"));
	});
});
