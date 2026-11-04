import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { acpRuntimeAdapterPath, repairAcpRuntime, type ForkScript } from "./acp-runtime-repair";

let appPath: string;
beforeEach(() => {
	appPath = mkdtempSync(path.join(tmpdir(), "acp-repair-"));
	vi.spyOn(console, "warn").mockImplementation(() => undefined);
	vi.spyOn(console, "error").mockImplementation(() => undefined);
});
afterEach(() => {
	rmSync(appPath, { recursive: true, force: true });
	vi.restoreAllMocks();
});

function withBuildScript() {
	mkdirSync(path.join(appPath, "scripts"), { recursive: true });
	writeFileSync(path.join(appPath, "scripts", "build-acp-runtime.mjs"), "");
}

describe("repairAcpRuntime", () => {
	it("forks the build script without ELECTRON_RUN_AS_NODE when the adapter is missing", async () => {
		withBuildScript();
		const fork = vi.fn<ForkScript>((_script, _options) => ({
			once: (event: string, listener: (code: number) => void) => {
				if (event === "exit") queueMicrotask(() => listener(0));
			},
		}));
		await repairAcpRuntime(appPath, fork, { PATH: "/bin", ELECTRON_RUN_AS_NODE: "1", npm_execpath: "/x/npm-cli.js" });
		expect(fork).toHaveBeenCalledTimes(1);
		const [script, options] = fork.mock.calls[0];
		expect(script).toBe(path.join(appPath, "scripts", "build-acp-runtime.mjs"));
		expect(options.cwd).toBe(appPath);
		expect(options.env).not.toHaveProperty("ELECTRON_RUN_AS_NODE");
		expect(options.env).not.toHaveProperty("npm_execpath");
		expect(options.env.PATH).toBe("/bin");
	});

	it("does nothing when the adapter already exists", async () => {
		withBuildScript();
		const adapter = acpRuntimeAdapterPath(appPath);
		mkdirSync(path.dirname(adapter), { recursive: true });
		writeFileSync(adapter, "");
		const fork = vi.fn<ForkScript>();
		await repairAcpRuntime(appPath, fork);
		expect(fork).not.toHaveBeenCalled();
	});

	it("resolves when fork throws", async () => {
		withBuildScript();
		const fork = vi.fn<ForkScript>(() => {
			throw new Error("boom");
		});
		await expect(repairAcpRuntime(appPath, fork)).resolves.toBeUndefined();
	});
});
