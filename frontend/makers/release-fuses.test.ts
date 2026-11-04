import { FuseV1Options } from "@electron/fuses";
import { describe, expect, it } from "vitest";
import { FuseState, RELEASE_FUSES, diffFuses } from "./release-fuses";

const intended = {
	[FuseV1Options.RunAsNode]: FuseState.DISABLE,
	[FuseV1Options.EnableCookieEncryption]: FuseState.ENABLE,
	[FuseV1Options.EnableNodeOptionsEnvironmentVariable]: FuseState.DISABLE,
	[FuseV1Options.EnableNodeCliInspectArguments]: FuseState.DISABLE,
	[FuseV1Options.EnableEmbeddedAsarIntegrityValidation]: FuseState.ENABLE,
	[FuseV1Options.OnlyLoadAppFromAsar]: FuseState.ENABLE,
	[FuseV1Options.LoadBrowserProcessSpecificV8Snapshot]: FuseState.DISABLE,
	[FuseV1Options.GrantFileProtocolExtraPrivileges]: FuseState.DISABLE,
};

describe("diffFuses", () => {
	it("accepts the intended wire", () => {
		expect(diffFuses(intended)).toEqual([]);
	});
	it("flags cookie encryption left off", () => {
		expect(diffFuses({ ...intended, [FuseV1Options.EnableCookieEncryption]: FuseState.DISABLE })).toEqual([
			"fuse EnableCookieEncryption is off, expected on",
		]);
	});
	it("flags NODE_OPTIONS and RunAsNode left on", () => {
		const problems = diffFuses({
			...intended,
			[FuseV1Options.RunAsNode]: FuseState.ENABLE,
			[FuseV1Options.EnableNodeOptionsEnvironmentVariable]: FuseState.ENABLE,
		});
		expect(problems).toHaveLength(2);
	});
	it("treats a missing fuse as unset, not as satisfied", () => {
		expect(diffFuses({})).toHaveLength(Object.keys(RELEASE_FUSES).length - 1);
	});
});

describe("RELEASE_FUSES", () => {
	it("pins the hardened set", () => {
		expect(RELEASE_FUSES[FuseV1Options.EnableCookieEncryption]).toBe(true);
		expect(RELEASE_FUSES[FuseV1Options.EnableNodeOptionsEnvironmentVariable]).toBe(false);
		expect(RELEASE_FUSES[FuseV1Options.RunAsNode]).toBe(false);
	});
});
