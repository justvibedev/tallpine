import { spawnSync } from "node:child_process";

// Pure: does `codesign -dvv` output (stderr) show the hardened runtime?
// codesign prints e.g. "flags=0x10000(runtime)"; an ad hoc build prints
// "flags=0x2(adhoc)" or "flags=0x20002(adhoc,linker-signed)".
export function hasHardenedRuntime(codesignDetails: string): boolean {
	const match = /^CodeDirectory .*flags=0x[0-9a-f]+\(([^)]*)\)/m.exec(codesignDetails);
	return match !== null && match[1].split(",").includes("runtime");
}

export function isAdHoc(codesignDetails: string): boolean {
	return /^Signature=adhoc$/m.test(codesignDetails) || /flags=0x[0-9a-f]+\([^)]*\badhoc\b/.test(codesignDetails);
}

// TALLPINE_ALLOW_UNSIGNED=1 is the single loud opt-out for local/test packaging.
export function unsignedAllowed(env: NodeJS.ProcessEnv): boolean {
	return env.TALLPINE_ALLOW_UNSIGNED === "1";
}

export type CodesignRunner = (args: string[]) => { status: number | null; output: string };

const runCodesign: CodesignRunner = (args) => {
	const result = spawnSync("codesign", args, { encoding: "utf8" });
	return { status: result.status, output: `${result.stdout ?? ""}${result.stderr ?? ""}` };
};

// Decide whether a packaged macOS app may be turned into a release artifact.
// `signingConfigured` says whether signing material was supplied to the build.
// Returns warnings to print (unsigned allowed); throws when it must not ship.
export function assertMacSigning(
	appBundle: string,
	signingConfigured: boolean,
	env: NodeJS.ProcessEnv = process.env,
	codesign: CodesignRunner = runCodesign,
): string[] {
	if (!signingConfigured) {
		if (unsignedAllowed(env)) {
			return [
				`!!! UNSIGNED BUILD (TALLPINE_ALLOW_UNSIGNED=1): ${appBundle} is NOT a releasable artifact. Local/test use only. !!!`,
			];
		}
		throw new Error(
			`[release] ${appBundle} is not signed: no signing identity was configured. Refusing to produce a release artifact. ` +
				"Configure a signing identity, or set TALLPINE_ALLOW_UNSIGNED=1 for an explicit local/test build.",
		);
	}
	const verify = codesign(["--verify", "--deep", "--strict", appBundle]);
	if (verify.status !== 0) {
		throw new Error(`[release] codesign --verify --deep --strict failed for ${appBundle}: ${verify.output.trim()}`);
	}
	const details = codesign(["-dvv", appBundle]);
	if (details.status !== 0) {
		throw new Error(`[release] codesign -dvv failed for ${appBundle}: ${details.output.trim()}`);
	}
	if (isAdHoc(details.output)) {
		throw new Error(`[release] ${appBundle} is signed ad hoc, not with a signing identity`);
	}
	if (!hasHardenedRuntime(details.output)) {
		throw new Error(`[release] ${appBundle} is signed without the hardened runtime (codesign flags lack "runtime")`);
	}
	return [];
}
