import { mkdir, mkdtemp, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { cleanupTemporaryBrowserPartitions } from "./temporary-partition-cleanup";

let userData: string;

beforeEach(async () => {
	userData = await mkdtemp(path.join(tmpdir(), "partition-cleanup-"));
});

afterEach(async () => {
	await rm(userData, { recursive: true, force: true });
});

describe("cleanupTemporaryBrowserPartitions", () => {
	it("removes ao- and vo- temporary partitions and keeps everything else", async () => {
		const partitions = path.join(userData, "Partitions");
		for (const name of [
			"ao-browser-temporary-1111",
			"vo-browser-temporary-2222",
			"AO-Browser-Temporary-3333",
			"ao-browser-profile-work",
			"default-profile",
		]) {
			await mkdir(path.join(partitions, name), { recursive: true });
			await writeFile(path.join(partitions, name, "Cookies"), "x");
		}
		await writeFile(path.join(partitions, "ao-browser-temporary-file"), "x");

		expect(await cleanupTemporaryBrowserPartitions(userData)).toBe(4);
		expect((await readdir(partitions)).sort()).toEqual(["ao-browser-profile-work", "default-profile"]);
	});

	it("is a no-op when there is no Partitions directory", async () => {
		expect(await cleanupTemporaryBrowserPartitions(userData)).toBe(0);
	});
});
