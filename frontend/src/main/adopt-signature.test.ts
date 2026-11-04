import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import { adoptionSignatureError, bundledHelperPaths } from "./adopt-signature";
import type { CodesignRunner } from "./helper-signature";

const opts = { isPackaged: true, platform: "darwin" as const, teamId: "ABCDE12345" };

function bundle() {
	const res = mkdtempSync(path.join(tmpdir(), "adopt-"));
	const daemon = path.join(res, "daemon", "tallpine-daemon");
	mkdirSync(path.dirname(daemon), { recursive: true });
	writeFileSync(daemon, "x");
	return { res, daemon };
}

describe("adoptionSignatureError", () => {
	it("refuses to adopt a daemon whose binary fails verification", async () => {
		const { res, daemon } = bundle();
		const run: CodesignRunner = async () => ({ code: 3, stderr: "a.out: invalid signature\n" });
		const err = await adoptionSignatureError({ source: "bundled", command: daemon }, res, { ...opts, run, cache: new Map() }, () => {});
		expect(err).toContain(daemon);
		expect(err).toContain("code-signature check");
	});

	it("allows a verified daemon", async () => {
		const { res, daemon } = bundle();
		const run = vi.fn<CodesignRunner>(async () => ({ code: 0, stderr: "" }));
		expect(await adoptionSignatureError({ source: "bundled", command: daemon }, res, { ...opts, run, cache: new Map() }, () => {})).toBeNull();
		expect(run).toHaveBeenCalled();
	});

	it("skips non-bundled daemons", async () => {
		const run = vi.fn<CodesignRunner>(async () => ({ code: 3, stderr: "" }));
		expect(await adoptionSignatureError({ source: "env", command: "/x" }, "/r", { ...opts, run }, () => {})).toBeNull();
		expect(run).not.toHaveBeenCalled();
	});

	it("lists only existing helpers", () => {
		const { res, daemon } = bundle();
		expect(bundledHelperPaths(daemon, res, "darwin")).toEqual([daemon]);
	});
});
