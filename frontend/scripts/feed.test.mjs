// @vitest-environment node
import { describe, expect, it } from "vitest";
import { existsSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { buildYml, feedFilenames, generateMacFeed, selectMacZips, MAC_MINIMUM_DARWIN_VERSION } from "./feed.mjs";

describe("selectMacZips", () => {
	it("keeps versioned darwin zips, arm64 first", () => {
		expect(selectMacZips(["Tallpine-1.2.0.dmg", "Tallpine-darwin-x64-1.2.0.zip", "Tallpine-darwin-arm64-1.2.0.zip", "Tallpine-darwin-arm64-1.1.0.zip"], "1.2.0")).toEqual([
			"Tallpine-darwin-arm64-1.2.0.zip",
			"Tallpine-darwin-x64-1.2.0.zip",
		]);
	});
});

describe("feedFilenames", () => {
	it("writes latest-mac.yml, plus the channel file for a prerelease", () => {
		expect(feedFilenames("1.2.0")).toEqual(["latest-mac.yml"]);
		expect(feedFilenames("1.3.0-beta.2")).toEqual(["latest-mac.yml", "beta-mac.yml"]);
	});
});

describe("generateMacFeed", () => {
	it("writes latest-mac.yml and the zip's blockmap", async () => {
		const dir = mkdtempSync(join(tmpdir(), "feed-"));
		const zip = "Tallpine-darwin-arm64-1.2.0.zip";
		const data = Buffer.alloc(100_000, 7);
		writeFileSync(join(dir, zip), data);
		expect(await generateMacFeed(dir, "1.2.0", "2026-10-30T00:00:00.000Z")).toEqual(["latest-mac.yml"]);
		expect(existsSync(join(dir, `${zip}.blockmap`))).toBe(true);
		const sha = createHash("sha512").update(data).digest("base64");
		expect(readFileSync(join(dir, "latest-mac.yml"), "utf8")).toBe(
			buildYml("1.2.0", [{ url: zip, sha512: sha, size: data.length }], "2026-10-30T00:00:00.000Z"),
		);
		expect(readFileSync(join(dir, "latest-mac.yml"), "utf8")).toContain(`minimumSystemVersion: ${MAC_MINIMUM_DARWIN_VERSION}`);
	});

	it("refuses a directory without the zip", async () => {
		const dir = mkdtempSync(join(tmpdir(), "feed-"));
		await expect(generateMacFeed(dir, "1.2.0", "x")).rejects.toThrow(/no darwin zip/);
	});
});
