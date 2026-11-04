import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { extraResourcesForPlatform } from "./forge.config";

const root = path.resolve(__dirname, "..");
const manifest = readFileSync(path.join(root, "scripts/bundled-helpers.txt"), "utf8")
	.split("\n")
	.filter((l) => l.trim() && !l.startsWith("#"));
const forge = readFileSync(path.join(root, "frontend/forge.config.ts"), "utf8");
const brand = readFileSync(path.join(root, "backend/internal/brand/brand.go"), "utf8");

describe("scripts/bundled-helpers.txt", () => {
	it("only lists helpers inside a directory forge packages on darwin", () => {
		const packaged = extraResourcesForPlatform("darwin");
		for (const rel of manifest) expect(packaged, rel).toContain(rel.split("/")[0] === "acp-runtime" ? "resources/acp-runtime" : rel.split("/")[0]);
	});
	it("names the daemon after the CLI brand", () => {
		const cli = /CLIName\s*=\s*"([^"]+)"/.exec(brand)?.[1];
		expect(manifest).toContain(`daemon/${cli}`);
	});
	it("covers every path adopt-signature verifies and forge's postPackage checks", () => {
		const adopt = readFileSync(path.join(__dirname, "src/main/adopt-signature.ts"), "utf8");
		const joined = [...adopt.matchAll(/path\.join\(resourcesPath, ([^)]*)\)/g)].map((m) => m[1].replace(/["\s]/g, "").split(",").join("/"));
		expect(joined.length).toBeGreaterThan(0);
		for (const rel of joined) expect(manifest, rel).toContain(rel);
		expect(forge).toContain('"wake-helper", "tallpine-wake"');
		expect(forge).toContain('"cloudflared", "cloudflared"');
		expect(forge).toContain('"tmux", "bin", "tmux"');
	});
});
