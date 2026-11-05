// Type declarations for build-info.mjs, a plain ESM module that node runs
// directly from build-daemon.mjs and that forge.config.ts and
// vite.main.config.ts import.
export interface BuildInfo {
	version: string;
	commit: string;
	date: string;
	dirty: "true" | "false";
	kind: "local" | "release";
}
export declare const BUILDINFO_PACKAGE: string;
export declare function resolveBuildInfo(options?: {
	env?: NodeJS.ProcessEnv;
	cwd?: string;
	now?: Date;
}): BuildInfo;
export declare function releaseSourceLdflags(env?: NodeJS.ProcessEnv): string[];
export declare function goLdflags(info: BuildInfo, env?: NodeJS.ProcessEnv): string;
export declare function teamIdFromIdentity(identity: string | undefined): string;
