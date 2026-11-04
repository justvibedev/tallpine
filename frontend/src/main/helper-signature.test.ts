import { copyFileSync, existsSync, mkdtempSync, readFileSync, rmSync, utimesSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import { type CodesignRunner, ensureVerifiedStagedCopy, teamRequirement, verifyBundledHelpers, verifyHelperSignature } from "./helper-signature";

const TEAM = "ABCDE12345";
function tmpFile(): string {
	const f = path.join(mkdtempSync(path.join(tmpdir(), "sig-")), "helper");
	writeFileSync(f, "x");
	return f;
}
const base = { isPackaged: true, platform: "darwin" as const, teamId: TEAM };

describe("verifyHelperSignature", () => {
	it("verifies a valid binary with the team requirement", async () => {
		const run = vi.fn<CodesignRunner>(async () => ({ code: 0, stderr: "" }));
		const f = tmpFile();
		const r = await verifyHelperSignature(f, { ...base, run, cache: new Map() });
		expect(r.status).toBe("verified");
		expect(run.mock.calls[0][0]).toContain(`-R=${teamRequirement(TEAM)}`);
		expect(teamRequirement(TEAM)).toBe(`anchor apple generic and certificate leaf[subject.OU] = "${TEAM}"`);
	});

	it("fails an invalid/replaced binary", async () => {
		const run: CodesignRunner = async () => ({ code: 3, stderr: "test: code failed to satisfy specified code requirement(s)\n" });
		const r = await verifyHelperSignature(tmpFile(), { ...base, run, cache: new Map() });
		expect(r).toMatchObject({ status: "failed", reason: expect.stringContaining("failed to satisfy") });
	});

	it("fails a missing file", async () => {
		const r = await verifyHelperSignature("/nonexistent/helper", { ...base, run: async () => ({ code: 0, stderr: "" }), cache: new Map() });
		expect(r.status).toBe("failed");
	});

	it("skips visibly for dev, non-darwin and unsigned builds without running codesign", async () => {
		const run = vi.fn<CodesignRunner>(async () => ({ code: 1, stderr: "" }));
		const f = tmpFile();
		for (const o of [{ isPackaged: false }, { platform: "linux" as const }, { teamId: "" }]) {
			const r = await verifyHelperSignature(f, { ...base, ...o, run, cache: new Map() });
			expect(r.status).toBe("skipped");
			expect((r as { reason: string }).reason).not.toBe("");
		}
		expect(run).not.toHaveBeenCalled();
	});

	it("caches by inode+mtime and re-verifies after the file changes", async () => {
		const run = vi.fn<CodesignRunner>(async () => ({ code: 0, stderr: "" }));
		const cache = new Map<string, string>();
		const f = tmpFile();
		await verifyHelperSignature(f, { ...base, run, cache });
		const second = await verifyHelperSignature(f, { ...base, run, cache });
		expect(second).toMatchObject({ status: "verified", cached: true });
		expect(run).toHaveBeenCalledTimes(1);
		utimesSync(f, new Date(), new Date(Date.now() + 5000));
		await verifyHelperSignature(f, { ...base, run, cache });
		expect(run).toHaveBeenCalledTimes(2);
	});

	it("does not cache a failure", async () => {
		const run = vi.fn<CodesignRunner>(async () => ({ code: 3, stderr: "bad" }));
		const cache = new Map<string, string>();
		const f = tmpFile();
		await verifyHelperSignature(f, { ...base, run, cache });
		await verifyHelperSignature(f, { ...base, run, cache });
		expect(run).toHaveBeenCalledTimes(2);
	});
});

describe("verifyBundledHelpers", () => {
	it("returns an error naming the path and reinstall guidance on the first failure", async () => {
		const good = tmpFile();
		const bad = tmpFile();
		const run: CodesignRunner = async (args) => (args[args.length - 1] === bad ? { code: 3, stderr: "bad" } : { code: 0, stderr: "" });
		const msg = await verifyBundledHelpers([good, bad], { ...base, run, cache: new Map() }, () => undefined);
		expect(msg).toContain(bad);
		expect(msg).toContain("Reinstall");
	});

	it("returns null when all pass or are skipped", async () => {
		const logs: string[] = [];
		const msg = await verifyBundledHelpers([tmpFile()], { ...base, isPackaged: false }, (m) => logs.push(m));
		expect(msg).toBeNull();
		expect(logs[0]).toContain("skipped");
	});
});

describe("ensureVerifiedStagedCopy", () => {
	function setup() {
		const dir = mkdtempSync(path.join(tmpdir(), "stage-"));
		const source = path.join(dir, "src");
		const destination = path.join(dir, "dest");
		writeFileSync(source, "good");
		const ops = {
			exists: (p: string) => existsSync(p),
			remove: async (p: string) => rmSync(p, { force: true }),
			copy: async (s: string, d: string) => copyFileSync(s, d),
		};
		return { source, destination, ops };
	}
	// A binary "verifies" when its content is "good".
	const run: CodesignRunner = async (args) =>
		readFileSync(args[args.length - 1], "utf8") === "good" ? { code: 0, stderr: "" } : { code: 3, stderr: "tampered" };

	it("re-stages a tampered staged copy from the verified source", async () => {
		const { source, destination, ops } = setup();
		writeFileSync(destination, "evil");
		const err = await ensureVerifiedStagedCopy(source, destination, { ...base, run, cache: new Map() }, ops);
		expect(err).toBeNull();
		expect(readFileSync(destination, "utf8")).toBe("good");
	});

	it("refuses when the bundled source itself fails", async () => {
		const { source, destination, ops } = setup();
		writeFileSync(source, "evil");
		const err = await ensureVerifiedStagedCopy(source, destination, { ...base, run, cache: new Map() }, ops);
		expect(err).toContain(source);
		expect(existsSync(destination)).toBe(false);
	});

	it("keeps a valid staged copy without copying", async () => {
		const { source, destination, ops } = setup();
		writeFileSync(destination, "good");
		const copy = vi.fn(ops.copy);
		expect(await ensureVerifiedStagedCopy(source, destination, { ...base, run, cache: new Map() }, { ...ops, copy })).toBeNull();
		expect(copy).not.toHaveBeenCalled();
	});
});
