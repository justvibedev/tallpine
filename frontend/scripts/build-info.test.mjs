// @vitest-environment node
import { spawnSync } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { BUILDINFO_PACKAGE, goLdflags, resolveBuildInfo } from "./build-info.mjs";

describe("resolveBuildInfo", () => {
	it("uses TALLPINE_BUILD_* overrides verbatim", () => {
		const info = resolveBuildInfo({
			env: { TALLPINE_BUILD_VERSION: "1.2.3", TALLPINE_BUILD_COMMIT: "abc", TALLPINE_BUILD_DATE: "2026-10-26T00:00:00Z", TALLPINE_BUILD_DIRTY: "true" },
		});
		expect(info).toEqual({ version: "1.2.3", commit: "abc", date: "2026-10-26T00:00:00Z", dirty: "true", kind: "release" });
	});

	it("falls back to package.json, no commit and a clean flag outside a git checkout", () => {
		const info = resolveBuildInfo({ env: {}, cwd: mkdtempSync(join(tmpdir(), "vo-build-info-")), now: new Date("2026-10-26T01:02:03.456Z") });
		expect(info.version).toMatch(/^\d+\.\d+\.\d+/);
		expect(info.commit).toBe("");
		expect(info.dirty).toBe("false");
		expect(info.date).toBe("2026-10-26T01:02:03Z");
	});

	const inGit = spawnSync("git", ["rev-parse", "HEAD"], { cwd: import.meta.dirname }).status === 0;
	it.skipIf(!inGit)("reads the commit from git in a checkout", () => {
		const info = resolveBuildInfo({ env: {} });
		expect(info.commit).toMatch(/^[0-9a-f]{40}$/);
	});
});

describe("goLdflags", () => {
	it("sets every buildinfo variable", () => {
		expect(goLdflags({ version: "1.2.3", commit: "abc", date: "d", dirty: "false" }, {})).toBe(
			`-X ${BUILDINFO_PACKAGE}.Version=1.2.3 -X ${BUILDINFO_PACKAGE}.Commit=abc -X ${BUILDINFO_PACKAGE}.Date=d -X ${BUILDINFO_PACKAGE}.Dirty=false`,
		);
	});

	const info = { version: "1.2.3", commit: "abc", date: "d", dirty: "false" };
	const cli = "tallpine.eu/tallpine/backend/internal/cli";
	const signer = "tallpine.eu/tallpine/backend/internal/releasesigner";

	it("injects the release repo and Team ID when the env is set", () => {
		const out = goLdflags(info, {
			TALLPINE_RELEASE_GITHUB_REPO: "example-owner/example-repo",
			TALLPINE_SIGN_IDENTITY: "Developer ID Application: Example Co (ABCDE12345)",
		});
		expect(out).toContain(`-X ${cli}.releaseRepo=example-owner/example-repo`);
		expect(out).toContain(`-X ${signer}.ExpectedTeamID=ABCDE12345`);
		expect(out).toContain(`-X ${BUILDINFO_PACKAGE}.TeamID=ABCDE12345`);
	});

	it("rejects a release repo that is not an owner/repo slug (e.g. a checkout path)", () => {
		for (const bad of ["/path/to/checkout", "owner", "a/b/c", "owner/repo with space"]) {
			expect(() => goLdflags(info, { TALLPINE_RELEASE_GITHUB_REPO: bad })).toThrow(/owner\/repo/);
		}
	});

	it("falls back to APPLE_SIGNING_IDENTITY for the Team ID", () => {
		expect(goLdflags(info, { APPLE_SIGNING_IDENTITY: "Name (ABCDE12345)" })).toContain(`${signer}.ExpectedTeamID=ABCDE12345`);
	});

	it("omits both flags when the env is not set or the identity has no Team ID", () => {
		for (const env of [{}, { TALLPINE_SIGN_IDENTITY: "-" }, { TALLPINE_SIGN_IDENTITY: "Name (short)" }]) {
			const out = goLdflags(info, env);
			expect(out).not.toContain("releaseRepo");
			expect(out).not.toContain("ExpectedTeamID");
		}
	});
});

describe("build kind", () => {
	it("is local only when TALLPINE_BUILD_KIND=local", () => {
		expect(resolveBuildInfo({ env: { TALLPINE_BUILD_VERSION: "1", TALLPINE_BUILD_COMMIT: "a", TALLPINE_BUILD_DIRTY: "false", TALLPINE_BUILD_KIND: "local" } }).kind).toBe("local");
		expect(resolveBuildInfo({ env: { TALLPINE_BUILD_VERSION: "1", TALLPINE_BUILD_COMMIT: "a", TALLPINE_BUILD_DIRTY: "false" } }).kind).toBe("release");
	});
});
