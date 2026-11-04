// @vitest-environment node
import { execFileSync } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { builtinModules } from "node:module";
import os from "node:os";
import path from "node:path";
import { build } from "vite";
import { expect, it } from "vitest";

it("loads the browser import module at CommonJS startup without loading SQLite", async () => {
	const scratch = await mkdtemp(path.join(os.tmpdir(), "vo-import-startup-"));
	try {
		// Vitest's source transform accepts import.meta.url. Forge's CommonJS
		// main bundle does not, so compile and execute that boundary separately.
		await build({
			configFile: path.resolve("vite.main.config.ts"),
			logLevel: "silent",
			build: {
				outDir: scratch,
				lib: {
					entry: path.resolve("src/main/browser-profile-import/service.ts"),
					formats: ["cjs"],
					fileName: () => "browser-profile-import.cjs",
				},
				rollupOptions: {
					external: builtinModules.flatMap((id) => [id, `node:${id}`]),
				},
			},
		});
		const output = execFileSync(process.execPath, ["-e", `
			const assert = require("node:assert/strict");
			const { BrowserProfileImportService } = require(process.argv[1]);
			assert.equal(typeof BrowserProfileImportService, "function");
			assert.equal(Object.keys(require.cache).some(file => file.includes("better-sqlite3")), false);
			console.log("CommonJS startup OK");
		`, path.join(scratch, "browser-profile-import.cjs")], {
			encoding: "utf8",
			timeout: 10_000,
			env: { ...process.env, TALLPINE_DATA_DIR: path.join(scratch, "data") },
		});
		expect(output.trim()).toBe("CommonJS startup OK");
	} finally {
		await rm(scratch, { recursive: true, force: true });
	}
});
