import { mkdir, mkdtemp, readdir, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { removeStaleAppSupportDir } from "./legacy-app-support-cleanup";

const dirs: string[] = [];
afterEach(async () => {
	for (const dir of dirs.splice(0)) await rm(dir, { recursive: true, force: true });
});

async function appSupport(entries: string[]): Promise<string> {
	const root = await mkdtemp(path.join(os.tmpdir(), "vo-appsupport-"));
	dirs.push(root);
	const dir = path.join(root, "Tallpine");
	await mkdir(path.join(dir, "GPUCache"), { recursive: true });
	for (const name of entries) await writeFile(path.join(dir, name), "x");
	return root;
}

describe("removeStaleAppSupportDir", () => {
	it("removes a directory that holds only caches and Local State", async () => {
		const root = await appSupport(["Local State"]);
		expect(await removeStaleAppSupportDir(root)).toBe(true);
		expect(await readdir(root)).toEqual([]);
	});

	it("leaves a directory alone when anything else is in it", async () => {
		const root = await appSupport(["Local State", "Cookies"]);
		expect(await removeStaleAppSupportDir(root)).toBe(false);
		expect(await readdir(path.join(root, "Tallpine"))).toContain("Cookies");
	});

	it("does nothing when the directory does not exist", async () => {
		const root = await mkdtemp(path.join(os.tmpdir(), "vo-appsupport-"));
		dirs.push(root);
		expect(await removeStaleAppSupportDir(root)).toBe(false);
	});
});
