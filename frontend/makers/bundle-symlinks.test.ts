import { mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { assertNoEscapingSymlinks, findEscapingSymlinks } from "./bundle-symlinks";

const dirs: string[] = [];
function fixture(): { root: string; outside: string } {
	const top = mkdtempSync(path.join(tmpdir(), "vo-symlinks-"));
	dirs.push(top);
	const root = path.join(top, "App.app");
	mkdirSync(path.join(root, "a", ".bin"), { recursive: true });
	writeFileSync(path.join(root, "a", "real.js"), "");
	writeFileSync(path.join(top, "outside.js"), "");
	return { root, outside: path.join(top, "outside.js") };
}
afterEach(() => { for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true }); });

describe("bundle symlink guard", () => {
	it("passes a relative in-bundle link", () => {
		const { root } = fixture();
		symlinkSync("../real.js", path.join(root, "a", ".bin", "tool"));
		expect(findEscapingSymlinks(root)).toEqual([]);
		expect(() => assertNoEscapingSymlinks(root)).not.toThrow();
	});
	it("flags an absolute out-of-bundle link", () => {
		const { root, outside } = fixture();
		symlinkSync(outside, path.join(root, "a", ".bin", "tool"));
		expect(findEscapingSymlinks(root)).toHaveLength(1);
		expect(() => assertNoEscapingSymlinks(root)).toThrow(/outside/);
	});
	it("flags a relative link escaping via ..", () => {
		const { root } = fixture();
		symlinkSync("../../../outside.js", path.join(root, "a", ".bin", "tool"));
		expect(findEscapingSymlinks(root)).toHaveLength(1);
	});
	it("flags an absolute link into the bundle's own path only if it stays inside", () => {
		const { root } = fixture();
		symlinkSync(path.join(root, "a", "real.js"), path.join(root, "a", ".bin", "tool"));
		expect(findEscapingSymlinks(root)).toEqual([]);
	});
});
