import { mkdtempSync, rmSync } from "node:fs";
import { mkdir, mkdtemp, readdir, rm, stat, utimes, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import { BROWSER_RUNTIME_RECLAIM_GRACE_MS, RUNTIME_OWNER_FILE, RUNTIME_OWNER_MARKER } from "./agent-browser-runtime/common";
import { AgentBrowserRuntime } from "./agent-browser-runtime/runtime";
import { reapAgentBrowserDaemons, reapOrphanedAgentBrowserProcesses } from "./agent-browser-runtime/reap";
import { scavengeBrowserRuntime } from "./agent-browser-runtime/scavenge";

describe.skipIf(process.platform === "win32")("agent-browser orphan daemon reaping (B679)", () => {
	it("SIGTERMs daemons of a dead owner's run root before removing it", async () => {
		const dataDir = await mkdtemp(path.join(os.tmpdir(), "tp-reap-"));
		try {
			const runDir = path.join(dataDir, "r-0123456789");
			const nsDir = path.join(runDir, "s", "namespaces", "tp-abcd");
			await mkdir(nsDir, { recursive: true });
			const ownerPath = path.join(runDir, RUNTIME_OWNER_FILE);
			await writeFile(ownerPath, JSON.stringify({ marker: RUNTIME_OWNER_MARKER, pid: 9869, startedAt: new Date().toISOString(), token: "a".repeat(32) }));
			await writeFile(path.join(nsDir, "default.pid"), "82157\n");
			const old = new Date(Date.now() - BROWSER_RUNTIME_RECLAIM_GRACE_MS - 60_000);
			await utimes(ownerPath, old, old);
			const signal = vi.fn();
			await scavengeBrowserRuntime(dataDir, () => false, undefined, { command: async () => "/x/agent-browser daemon", signal });
			expect(signal).toHaveBeenCalledWith(82157, "SIGTERM");
			await expect(stat(runDir)).rejects.toThrow();
		} finally {
			await rm(dataDir, { recursive: true, force: true });
		}
	});

	it("does not signal a reused pid that is not agent-browser", async () => {
		const dir = await mkdtemp(path.join(os.tmpdir(), "tp-reap-"));
		try {
			await writeFile(path.join(dir, "x.pid"), "4242");
			const signal = vi.fn();
			await reapAgentBrowserDaemons(dir, undefined, { command: async () => "/usr/bin/vim", signal });
			expect(signal).not.toHaveBeenCalled();
		} finally {
			await rm(dir, { recursive: true, force: true });
		}
	});

	describe("socket-owner fallback", () => {
		const bin = "/App/agent-browser";
		const run = async (procs: { pid: number; ppid: number; command: string }[], sockets: Record<number, string[]>, alive: number[]) => {
			const signal = vi.fn();
			await reapOrphanedAgentBrowserProcesses({
				binaryPath: bin,
				processAlive: (p) => alive.includes(p),
				listProcesses: async () => procs,
				listSockets: async (p) => sockets[p] ?? [],
				signal,
			});
			return signal;
		};
		const sock = (owner: number) => `/tmp/tp-br-${owner}-0123456789ab/namespaces/tp-x/run/tp-x.sock`;

		it("kills a reparented bundled daemon whose socket owner is dead", async () => {
			const signal = await run([{ pid: 82157, ppid: 1, command: bin }], { 82157: [sock(9869)] }, []);
			expect(signal).toHaveBeenCalledWith(82157, "SIGTERM");
		});

		it("spares live-owner, no-socket, non-init-parent and foreign-binary processes", async () => {
			const signal = await run(
				[
					{ pid: 1, ppid: 1, command: bin },
					{ pid: 2, ppid: 1, command: bin },
					{ pid: 3, ppid: 500, command: bin },
					{ pid: 4, ppid: 1, command: "/other/agent-browser" },
				],
				{ 1: [sock(83668)], 2: [], 3: [sock(9869)], 4: [sock(9869)] },
				[83668],
			);
			expect(signal).not.toHaveBeenCalled();
		});
	});
});

// The runtime links its short socket dir into /tmp (B462: denied in the security-mode sandbox).
const tmpWritable = (() => {
	try {
		rmSync(mkdtempSync("/tmp/tp-probe-"), { recursive: true });
		return true;
	} catch {
		return false;
	}
})();

describe.skipIf(process.platform === "win32" || !tmpWritable)("agent-browser shutdown reaping (B679)", () => {
	it("reaps the session daemon by pid file when native close fails, leaving other namespaces alone", async () => {
		const dataDir = await mkdtemp("/tmp/tp-reap-rt-");
		const signal = vi.fn();
		const processRunner = vi.fn(async (_binary: unknown, args: unknown, _env?: unknown) => {
			if ((args as string[])[0] === "close") throw new Error("close timed out");
			return { stdout: JSON.stringify({ success: true, data: {} }), stderr: "", exitCode: 0 };
		});
		const runtime = new AgentBrowserRuntime({
			binaryPath: process.execPath,
			dataDir,
			bridgeFactory: () => ({ start: async () => "ws://127.0.0.1:1/x", close: async () => undefined, reconcileTargets: () => undefined }),
			processRunner: processRunner as never,
			processAlive: () => false,
			reap: { listProcesses: async () => [], command: async () => "/x/agent-browser daemon", signal },
		});
		const provider = { listTargets: () => [], createTarget: async () => { throw new Error("unused"); }, activateTarget: () => undefined, closeTarget: () => undefined };
		try {
			await runtime.run("session-1", ["snapshot"], provider);
			const root = (await readdir(dataDir)).find((name) => name.startsWith("r-"))!;
			const ns = (processRunner.mock.calls[0][2] as NodeJS.ProcessEnv).AGENT_BROWSER_NAMESPACE!;
			for (const [dir, pid] of [[ns, "111"], ["tp-other", "222"]]) {
				const full = path.join(dataDir, root, "s", "namespaces", dir);
				await mkdir(full, { recursive: true });
				await writeFile(path.join(full, "d.pid"), pid);
			}
			await runtime.dispose();
			expect(signal).toHaveBeenCalledWith(111, "SIGTERM");
			expect(signal).not.toHaveBeenCalledWith(222, "SIGTERM");
		} finally {
			await rm(dataDir, { recursive: true, force: true });
		}
	});
});
