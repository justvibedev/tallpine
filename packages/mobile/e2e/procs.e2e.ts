import { spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { etimeSeconds, killProcessesUnder, processesUnder, reapStaleE2eProcesses } from "./procs";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const dirs: string[] = [];
afterEach(async () => {
	for (const d of dirs.splice(0)) { await killProcessesUnder(d); rmSync(d, { recursive: true, force: true }); }
});

/** A setsid-like orphan: reparented to init, argv carries `dir`, like a chat host. */
async function orphanUnder(dir: string): Promise<void> {
	spawn("sh", ["-c", `node -e "setTimeout(()=>{},300000)" ${dir}/data & exit 0`], { detached: true, stdio: "ignore" }).unref();
	for (let i = 0; i < 50 && processesUnder(dir).length === 0; i++) await sleep(100);
}

describe("e2e process reaping (B563)", () => {
	it("parses ps etime", () => {
		expect(etimeSeconds("05:07")).toBe(307);
		expect(etimeSeconds("01:02:03")).toBe(3723);
		expect(etimeSeconds("03-00:29:54")).toBe(3 * 86400 + 29 * 60 + 54);
	});

	it("killProcessesUnder reaps a detached orphan", async () => {
		const dir = mkdtempSync(join(tmpdir(), "tpe-"));
		dirs.push(dir);
		await orphanUnder(dir);
		expect(processesUnder(dir).length).toBeGreaterThan(0);
		await killProcessesUnder(dir);
		expect(processesUnder(dir)).toEqual([]);
	});

	it("reapStaleE2eProcesses kills old tpe-run orphans but not young ones", async () => {
		const dir = mkdtempSync(join(tmpdir(), "tpe-run-"));
		dirs.push(dir);
		await orphanUnder(dir);
		expect(reapStaleE2eProcesses(3600, dir)).toEqual([]); // too young
		expect(processesUnder(dir).length).toBeGreaterThan(0);
		await sleep(1100);
		expect(reapStaleE2eProcesses(1, dir).length).toBeGreaterThan(0);
		await sleep(300);
		expect(processesUnder(dir)).toEqual([]);
	});
});
