import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { resolveUserDataDir } from "./user-data-path";

const home = "/Users/someone";

describe("userData location", () => {
	it("stays under ~/.tallpine for packaged and dev runs, never ~/Library", () => {
		for (const dir of [resolveUserDataDir({ isPackaged: true, home }), resolveUserDataDir({ isPackaged: false, home })]) {
			expect(dir.startsWith(path.join(home, ".tallpine") + path.sep)).toBe(true);
			expect(dir).not.toContain("Library");
		}
	});

	it("lets only dev runs override it", () => {
		expect(resolveUserDataDir({ isPackaged: false, home, devOverride: "/tmp/dev" })).toBe("/tmp/dev");
		expect(resolveUserDataDir({ isPackaged: true, home, devOverride: "/tmp/dev" })).toBe(path.join(home, ".tallpine", "electron"));
	});

	// The cheap smoke test: Electron derives its caches, cookies and crash dumps
	// from userData when first asked, so the pin must run before anything else
	// in the entry point touches app paths or starts the app.
	it("is pinned in app-bootstrap, the first module main.ts imports, before the app can use any path", () => {
		const entry = readFileSync(path.join(__dirname, "..", "main.ts"), "utf8");
		const firstImport = /^import\s+["']([^"']+)["']/m.exec(entry);
		expect(firstImport?.[1]).toBe("./main/app-bootstrap");
		const source = readFileSync(path.join(__dirname, "app-bootstrap.ts"), "utf8");
		const pin = source.indexOf('app.setPath(\n\t"userData"');
		expect(pin).toBeGreaterThan(-1);
		for (const early of ["app.requestSingleInstanceLock(", "app.whenReady().then(", 'getPath("userData")', "crashReporter.start("]) {
			const at = source.indexOf(early);
			if (at !== -1) expect(at, early).toBeGreaterThan(pin);
		}
	});
});
