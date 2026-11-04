import { FuseV1Options, FuseVersion, getCurrentFuseWire } from "@electron/fuses";

// Fuse wire bytes; @electron/fuses does not export its FuseState enum.
export const FuseState = { DISABLE: 48, ENABLE: 49, REMOVED: 114, INHERIT: 144 } as const;
type FuseStateValue = number;

// The fuse set every packaged build must carry. forge.config.ts flips exactly
// this at package time and the post-package hook reads the packaged binary
// back against it, so a plugin change, an Electron upgrade or a hand edit
// cannot silently ship a weaker binary.
export const RELEASE_FUSES = {
	version: FuseVersion.V1,
	// The app never re-execs itself as Node (no ELECTRON_RUN_AS_NODE); acp-runtime
	// ships its own Node binary and the dev rebuild uses utilityProcess.
	[FuseV1Options.RunAsNode]: false,
	// Cookies of the browser-panel partitions are encrypted at rest with the
	// OS-held "Safe Storage" key. Existing plaintext rows keep being read.
	[FuseV1Options.EnableCookieEncryption]: true,
	// Off: Electron ignores NODE_OPTIONS (and unsets it for its children), so an
	// attacker or a stray launchctl setenv cannot inject --require into the
	// main process. Nothing in the app relies on it.
	[FuseV1Options.EnableNodeOptionsEnvironmentVariable]: false,
	[FuseV1Options.EnableNodeCliInspectArguments]: false,
	// Enforced on macOS and Windows; Electron does not validate ASAR
	// integrity on Linux.
	[FuseV1Options.EnableEmbeddedAsarIntegrityValidation]: true,
	[FuseV1Options.OnlyLoadAppFromAsar]: true,
	[FuseV1Options.GrantFileProtocolExtraPrivileges]: false,
} as const;

type FuseWire = Partial<Record<number, FuseStateValue>>;

// Returns one human-readable line per fuse whose read-back state differs from
// RELEASE_FUSES. Empty means the binary is fused as intended.
export function diffFuses(actual: FuseWire, expected: Record<string, unknown> = RELEASE_FUSES): string[] {
	const problems: string[] = [];
	for (const [key, want] of Object.entries(expected)) {
		const index = Number(key);
		if (!Number.isInteger(index)) continue; // "version"
		const name = FuseV1Options[index];
		const wantState = want ? FuseState.ENABLE : FuseState.DISABLE;
		const got = actual[index];
		if (got !== wantState) {
			problems.push(`fuse ${name} is ${describeState(got)}, expected ${describeState(wantState)}`);
		}
	}
	return problems;
}

function describeState(state: FuseStateValue | undefined): string {
	if (state === FuseState.ENABLE) return "on";
	if (state === FuseState.DISABLE) return "off";
	if (state === FuseState.REMOVED) return "removed";
	return "unset";
}

// Reads the fuses out of a packaged app (.app bundle, exe or ELF) and throws
// when any differ from RELEASE_FUSES.
export async function assertPackagedFuses(appPath: string): Promise<void> {
	const problems = diffFuses((await getCurrentFuseWire(appPath)) as FuseWire);
	if (problems.length > 0) {
		throw new Error(`[release] packaged binary fuses differ from the release set (${appPath}): ${problems.join("; ")}`);
	}
}
