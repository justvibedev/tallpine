import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { isMachOFile, macSignIgnore } from "./mac-sign-selection";

const thin = Buffer.from([0xcf, 0xfa, 0xed, 0xfe, 0x0c, 0x00, 0x00, 0x01, 0, 0, 0, 0]);
const fat = Buffer.from([0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 2, 0, 0, 0, 0]);
const javaClass = Buffer.from([0xca, 0xfe, 0xba, 0xbe, 0x00, 0x00, 0x00, 0x34]);

let dir: string;
beforeEach(() => {
	dir = mkdtempSync(join(tmpdir(), "mac-sign-sel-"));
});
afterEach(() => rmSync(dir, { recursive: true, force: true }));

function file(rel: string, bytes: Buffer | string): string {
	const p = join(dir, rel);
	mkdirSync(join(p, ".."), { recursive: true });
	writeFileSync(p, bytes);
	return p;
}

describe("isMachOFile", () => {
	it("accepts thin and fat Mach-O, rejects data and Java class files", () => {
		expect(isMachOFile(file("a", thin))).toBe(true);
		expect(isMachOFile(file("b", fat))).toBe(true);
		expect(isMachOFile(file("c", javaClass))).toBe(false);
		expect(isMachOFile(file("d.png", Buffer.from([0x89, 0x50, 0x4e, 0x47, 1, 2, 3, 4, 5])))).toBe(false);
		expect(isMachOFile(file("e", ""))).toBe(false);
		expect(isMachOFile(join(dir, "missing"))).toBe(false);
	});
});

describe("macSignIgnore", () => {
	it("skips non-Mach-O files, never directories or Mach-O files", () => {
		expect(macSignIgnore(file("Resources/app.asar", Buffer.from("not macho at all")))).toBe(true);
		mkdirSync(join(dir, "Foo.framework"));
		expect(macSignIgnore(join(dir, "Foo.framework"))).toBe(false);
		expect(macSignIgnore(file("Frameworks/lib.dylib", thin))).toBe(false);
		expect(macSignIgnore(file("tmux/bin/tmux", fat))).toBe(false);
		expect(macSignIgnore(join(dir, "missing"))).toBe(false);
	});
});
