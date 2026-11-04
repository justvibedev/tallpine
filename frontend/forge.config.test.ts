import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { MachOParseError } from "./makers/macho-archs";

// postMake's dmg/zip branches only need to prove they call the right
// maker-dmg functions with the right gates; the functions' own behavior
// (sealDmg's credential matrix, verifyMacArtifact's script invocation) is
// covered by makers/maker-dmg.test.ts. Mocking the whole module keeps this
// suite from spawning codesign/xcrun/bash indirectly through the real chain.
// vi.mock's factory is hoisted above the rest of the file (including plain
// const declarations), so the mock fns themselves must go through vi.hoisted.
const { sealDmg, verifyDmg, verifyMacArtifact, isSigningConfigured } = vi.hoisted(() => ({
	sealDmg: vi.fn<(path: string) => Promise<boolean>>(),
	verifyDmg: vi.fn<(path: string) => Promise<void>>(async () => undefined),
	verifyMacArtifact: vi.fn<(path: string) => Promise<void>>(async () => undefined),
	isSigningConfigured: vi.fn<() => boolean>(),
}));
vi.mock("./makers/maker-dmg", async (importOriginal) => {
	const actual = await importOriginal<typeof import("./makers/maker-dmg")>();
	return { ...actual, sealDmg, verifyDmg, verifyMacArtifact, isSigningConfigured };
});

// postPackage's signing and fuse assertions inspect real bundles (codesign,
// the Electron binary); their own tests live in makers/. The fake Resources
// trees below would otherwise hit real codesign whenever the mocked
// isSigningConfigured says "signed", making the result depend on the host.
const { assertMacSigning, assertPackagedFuses } = vi.hoisted(() => ({
	assertMacSigning: vi.fn<(bundle: string, configured: boolean) => string[]>(() => []),
	assertPackagedFuses: vi.fn<(path: string) => Promise<void>>(async () => undefined),
}));
vi.mock("./makers/release-signing", () => ({ assertMacSigning }));
vi.mock("./makers/release-fuses", async (importOriginal) => ({
	...(await importOriginal<typeof import("./makers/release-fuses")>()),
	assertPackagedFuses,
}));

import config, { resolveNotaryProfile, adHocResignMacApp, setMacDisplayName, extraResourcesForPlatform, macSignOptionsForFile, MAC_APP_ENTITLEMENTS } from "./forge.config";

// Minimal synthetic Mach-O headers (thin little-endian + fat big-endian), the
// two on-disk layouts the signing selector must tell apart. Full parser
// coverage lives in makers/macho-archs.test.ts; here the fixtures exist so the
// per-file signing decision is exercised against real file bytes.
const CPU_TYPE_X86_64 = 0x01000007;
const CPU_TYPE_ARM64 = 0x0100000c;

function thinMachO(cputype: number): Buffer {
	const buffer = Buffer.alloc(16);
	buffer.writeUInt32LE(0xfeedfacf, 0);
	buffer.writeUInt32LE(cputype, 4);
	return buffer;
}

function fatMachO(entries: number[]): Buffer {
	const buffer = Buffer.alloc(8 + entries.length * 20);
	buffer.writeUInt32BE(0xcafebabe, 0);
	buffer.writeUInt32BE(entries.length, 4);
	entries.forEach((cputype, index) => {
		buffer.writeUInt32BE(cputype, 8 + index * 20);
	});
	return buffer;
}

function withHostArch<T>(arch: string, run: () => T): T {
	const descriptor = Object.getOwnPropertyDescriptor(process, "arch");
	Object.defineProperty(process, "arch", { get: () => arch, configurable: true });
	try {
		return run();
	} finally {
		if (descriptor) Object.defineProperty(process, "arch", descriptor);
	}
}

let fixtureDir: string;

// The nested Node must sit at the real bundle path shape: the endsWith gate
// and the content selector are two halves of one decision.
function acpNodeWith(contents: Buffer): string {
	const binDir = join(
		fixtureDir,
		"Tallpine.app",
		"Contents",
		"Resources",
		"acp-runtime",
		"node",
		"bin",
	);
	mkdirSync(binDir, { recursive: true });
	writeFileSync(join(binDir, "node"), contents);
	return join(binDir, "node");
}

beforeEach(() => {
	fixtureDir = mkdtempSync(join(tmpdir(), "forge-signing-"));
});

afterEach(() => {
	rmSync(fixtureDir, { recursive: true, force: true });
});

describe("native runtime resources", () => {
	beforeEach(() => {
		// These fixtures are not real bundles; keep the ad-hoc re-sign out of them.
		isSigningConfigured.mockReturnValue(true);
		assertMacSigning.mockClear();
	});

	it("runs the signing assertion with the configured flag before the resource checks", async () => {
		mkdirSync(join(fixtureDir, "Tallpine.app", "Contents", "Resources"), { recursive: true });
		const hook = config.hooks?.postPackage;
		if (typeof hook !== "function") throw new Error("postPackage hook missing");
		await expect(hook(config, { platform: "darwin", arch: "arm64", outputPaths: [fixtureDir] })).rejects.toThrow("packaged macOS wake helper missing");
		expect(assertMacSigning).toHaveBeenCalledWith(join(fixtureDir, "Tallpine.app"), true);
	});

	it("propagates a signing assertion failure", async () => {
		assertMacSigning.mockImplementationOnce(() => {
			throw new Error("[release] not signed");
		});
		mkdirSync(join(fixtureDir, "Tallpine.app", "Contents", "Resources"), { recursive: true });
		const hook = config.hooks?.postPackage;
		if (typeof hook !== "function") throw new Error("postPackage hook missing");
		await expect(hook(config, { platform: "darwin", arch: "arm64", outputPaths: [fixtureDir] })).rejects.toThrow("[release] not signed");
	});

	it("fails packaging when the macOS wake helper was not copied into Resources", async () => {
		mkdirSync(join(fixtureDir, "Tallpine.app", "Contents", "Resources"), { recursive: true });
		const hook = config.hooks?.postPackage;
		expect(hook).toBeTypeOf("function");
		if (typeof hook !== "function") return;
		await expect(hook(config, { platform: "darwin", arch: "arm64", outputPaths: [fixtureDir] })).rejects.toThrow("packaged macOS wake helper missing");
	});

	it("bundles the wake coordinator only on macOS", () => {
		expect(extraResourcesForPlatform("darwin")).toContain("wake-helper");
		expect(extraResourcesForPlatform("linux")).not.toContain("wake-helper");
		expect(extraResourcesForPlatform("win32")).not.toContain("wake-helper");
	});

	it("bundles cloudflared only on macOS", () => {
		expect(extraResourcesForPlatform("darwin")).toContain("cloudflared");
		expect(extraResourcesForPlatform("linux")).not.toContain("cloudflared");
		expect(extraResourcesForPlatform("win32")).not.toContain("cloudflared");
	});

	it("fails packaging when cloudflared was not copied into Resources", async () => {
		const resources = join(fixtureDir, "Tallpine.app", "Contents", "Resources");
		mkdirSync(join(resources, "wake-helper"), { recursive: true });
		writeFileSync(join(resources, "wake-helper", "tallpine-wake"), "");
		const hook = config.hooks?.postPackage;
		if (typeof hook !== "function") throw new Error("postPackage hook missing");
		await expect(hook(config, { platform: "darwin", arch: "arm64", outputPaths: [fixtureDir] })).rejects.toThrow("packaged cloudflared missing");
	});

	it.each(["darwin", "linux"] as const)("bundles tmux on %s", (platform) => {
		expect(extraResourcesForPlatform(platform)).toContain("tmux");
	});

	it("does not bundle tmux on Windows", () => {
		expect(extraResourcesForPlatform("win32")).not.toContain("tmux");
	});
});

describe("adHocResignMacApp", () => {
	it("leaves a bundle alone when a real signing identity is configured", () => {
		isSigningConfigured.mockReturnValue(true);
		expect(adHocResignMacApp(join(fixtureDir, "missing.app"))).toBe(false);
	});

	it("re-signs unsigned local builds and surfaces codesign failures", () => {
		isSigningConfigured.mockReturnValue(false);
		expect(() => adHocResignMacApp(join(fixtureDir, "missing.app"))).toThrow("ad-hoc re-sign failed");
	});
});

type MacSignOptions = {
	identity?: string;
	optionsForFile?: typeof macSignOptionsForFile;
	ignore?: (filePath: string) => boolean;
};

async function loadMacSignOptions(env: {
	APPLE_SIGNING_IDENTITY?: string;
	CSC_LINK?: string;
}): Promise<MacSignOptions | undefined> {
	vi.stubEnv("APPLE_SIGNING_IDENTITY", env.APPLE_SIGNING_IDENTITY ?? "");
	vi.stubEnv("CSC_LINK", env.CSC_LINK ?? "");
	vi.resetModules();
	const { default: envConfig } = await import("./forge.config");
	return envConfig.packagerConfig?.osxSign as MacSignOptions | undefined;
}

afterEach(() => {
	vi.unstubAllEnvs();
	vi.resetModules();
	sealDmg.mockReset();
	verifyDmg.mockReset().mockImplementation(async () => undefined);
	verifyMacArtifact.mockReset().mockImplementation(async () => undefined);
	isSigningConfigured.mockReset();
});

describe("macOS signing", () => {
	const NODE_ENTITLEMENTS = [
		"com.apple.security.cs.allow-jit",
		"com.apple.security.cs.allow-unsigned-executable-memory",
	];

	it("wires the Mach-O-only ignore hook into osxSign, for both identity and CSC_LINK signing", async () => {
		const dir = mkdtempSync(join(tmpdir(), "forge-ignore-"));
		const asset = join(dir, "icon.png");
		writeFileSync(asset, "plain data, not Mach-O");
		try {
			for (const env of [{ APPLE_SIGNING_IDENTITY: "Developer ID Application: X (T)" }, { CSC_LINK: "/tmp/cert.p12" }]) {
				const options = await loadMacSignOptions(env);
				expect(options?.ignore?.(asset)).toBe(true);
				expect(options?.ignore?.(dir)).toBe(false);
			}
		} finally {
			rmSync(dir, { recursive: true, force: true });
		}
	});

	it("allows the bundled Node runtime to execute V8 JIT code on Intel Macs", () => {
		expect(macSignOptionsForFile(acpNodeWith(thinMachO(CPU_TYPE_X86_64)))).toEqual({
			entitlements: NODE_ENTITLEMENTS,
		});
	});

	it("keeps the override for a universal binary carrying an x86_64 slice", () => {
		expect(macSignOptionsForFile(acpNodeWith(fatMachO([CPU_TYPE_X86_64, CPU_TYPE_ARM64])))).toEqual({
			entitlements: NODE_ENTITLEMENTS,
		});
	});

	it("signs every other bundle file with the minimal entitlements plist", () => {
		const foreign = join(fixtureDir, "elsewhere", "tallpine");
		mkdirSync(join(fixtureDir, "elsewhere"), { recursive: true });
		writeFileSync(foreign, thinMachO(CPU_TYPE_X86_64));
		expect(macSignOptionsForFile(foreign)).toEqual({ entitlements: MAC_APP_ENTITLEMENTS });
	});

	it("uses the minimal (JIT-only) plist when the binary has no x86_64 slice", () => {
		expect(macSignOptionsForFile(acpNodeWith(thinMachO(CPU_TYPE_ARM64)))).toEqual({ entitlements: MAC_APP_ENTITLEMENTS });
		expect(macSignOptionsForFile(acpNodeWith(fatMachO([CPU_TYPE_ARM64])))).toEqual({ entitlements: MAC_APP_ENTITLEMENTS });
	});

	it("the minimal plist grants only JIT and the microphone for terminal CLIs", () => {
		const keys = [...readFileSync(MAC_APP_ENTITLEMENTS, "utf8").matchAll(/<key>([^<]+)<\/key>/g)].map((m) => m[1]);
		expect(keys).toEqual(["com.apple.security.cs.allow-jit", "com.apple.security.device.audio-input"]);
	});

	it("fails the signing pass when the file cannot be parsed", () => {
		expect(() => macSignOptionsForFile(acpNodeWith(Buffer.from("garbage header")))).toThrow(
			MachOParseError,
		);
		expect(() =>
			macSignOptionsForFile(acpNodeWith(Buffer.from([0xcf, 0xfa, 0xed, 0xfe]))),
		).toThrow(MachOParseError);
	});

	it("never consults the host architecture", () => {
		const acpNode = acpNodeWith(thinMachO(CPU_TYPE_ARM64));
		const fromX64Host = withHostArch("x64", () => macSignOptionsForFile(acpNode));
		const fromArm64Host = withHostArch("arm64", () => macSignOptionsForFile(acpNode));
		const fromAbsurdHost = withHostArch("ppc64", () => macSignOptionsForFile(acpNode));
		expect(fromX64Host).toEqual({ entitlements: MAC_APP_ENTITLEMENTS });
		expect(fromArm64Host).toEqual({ entitlements: MAC_APP_ENTITLEMENTS });
		expect(fromAbsurdHost).toEqual({ entitlements: MAC_APP_ENTITLEMENTS });
	});

	it.each([
		{
			name: "an explicit signing identity",
			env: { APPLE_SIGNING_IDENTITY: "Developer ID Application: Tallpine (TEAMID)" },
			identity: "Developer ID Application: Tallpine (TEAMID)",
		},
		{
			name: "a CSC_LINK certificate",
			env: { CSC_LINK: "base64-certificate" },
			identity: undefined,
		},
	])("wires the per-file override when signing with $name", async ({ env, identity }) => {
		const signOptions = await loadMacSignOptions(env);

		expect(signOptions?.identity).toBe(identity);
		expect(signOptions?.optionsForFile).toEqual(expect.any(Function));
	});

	it("leaves unsigned local packages unsigned", async () => {
		await expect(loadMacSignOptions({})).resolves.toBeUndefined();
	});
});

describe("postMake artifact verification", () => {
	// #3879: a valid outer seal did not prove the bundled Intel ACP Node could
	// actually run JavaScript. The dmg branch already ran verify-mac-artifact.sh
	// through sealDmg/verifyDmg; these cases prove the zip branch — the artifact
	// electron-updater installs auto-updates from, and per-arch what CI ships
	// for x64 — gets the same nested-Node check instead of shipping unverified.
	function darwinResult(artifacts: string[]) {
		return [{ artifacts, packageJSON: {}, platform: "darwin" as const, arch: "x64" as const }];
	}

	it("verifies a signed zip with the canonical script, gated on isSigningConfigured", async () => {
		isSigningConfigured.mockReturnValue(true);
		const makeResults = darwinResult(["/out/make/zip/tallpine-darwin-x64.zip"]);

		await config.hooks?.postMake?.({} as never, makeResults);

		expect(verifyMacArtifact).toHaveBeenCalledWith("/out/make/zip/tallpine-darwin-x64.zip");
	});

	it("skips zip verification for an unsigned local build", async () => {
		isSigningConfigured.mockReturnValue(false);
		const makeResults = darwinResult(["/out/make/zip/tallpine-darwin-x64.zip"]);

		await config.hooks?.postMake?.({} as never, makeResults);

		expect(verifyMacArtifact).not.toHaveBeenCalled();
	});

	it("still seals and verifies the dmg through the existing sealDmg/verifyDmg path", async () => {
		isSigningConfigured.mockReturnValue(true);
		sealDmg.mockResolvedValue(true);
		const makeResults = darwinResult(["/out/make/app.dmg"]);

		await config.hooks?.postMake?.({} as never, makeResults);

		expect(sealDmg).toHaveBeenCalledWith("/out/make/app.dmg");
		expect(verifyDmg).toHaveBeenCalledWith("/out/make/app.dmg");
		expect(verifyMacArtifact).not.toHaveBeenCalled();
	});

	it("verifies both the dmg and the zip when a make run produces both", async () => {
		isSigningConfigured.mockReturnValue(true);
		sealDmg.mockResolvedValue(true);
		const makeResults = darwinResult([
			"/out/make/zip/tallpine-darwin-x64.zip",
			"/out/make/app.dmg",
		]);

		await config.hooks?.postMake?.({} as never, makeResults);

		expect(verifyMacArtifact).toHaveBeenCalledWith("/out/make/zip/tallpine-darwin-x64.zip");
		expect(verifyDmg).toHaveBeenCalledWith("/out/make/app.dmg");
	});

	it("never verifies non-darwin artifacts", async () => {
		const makeResults = [
			{
				artifacts: ["/out/make/tallpine.exe"],
				packageJSON: {},
				platform: "win32" as const,
				arch: "x64" as const,
			},
		];

		await config.hooks?.postMake?.({} as never, makeResults);

		expect(sealDmg).not.toHaveBeenCalled();
		expect(verifyDmg).not.toHaveBeenCalled();
		expect(verifyMacArtifact).not.toHaveBeenCalled();
	});
});

describe("packaged native dependencies", () => {
	it("keeps the SQLite runtime available to the Vite main bundle", () => {
		const ignore = config.packagerConfig?.ignore;
		expect(ignore).toBeTypeOf("function");
		if (typeof ignore !== "function") return;

		expect(ignore("/.vite/build/main.cjs")).toBe(false);
		expect(ignore("/node_modules")).toBe(false);
		expect(ignore("/node_modules/better-sqlite3/build/Release/better_sqlite3.node")).toBe(false);
		expect(ignore("/node_modules/better-sqlite3")).toBe(false);
		expect(ignore("/node_modules/better-sqlite3/package.json")).toBe(false);
		expect(ignore("/node_modules/better-sqlite3/lib/index.js")).toBe(false);
		expect(ignore("/node_modules/better-sqlite3/lib/methods/wrappers.js")).toBe(false);
		// Build inputs and leftovers stay out of app.asar.
		for (const file of [
			"/node_modules/better-sqlite3/deps/sqlite3/sqlite3.c",
			"/node_modules/better-sqlite3/src/better_sqlite3.cpp",
			"/node_modules/better-sqlite3/bin/darwin-arm64-149/better-sqlite3.node",
			"/node_modules/better-sqlite3/build/Release/obj/gen/sqlite3/sqlite3.o",
			"/node_modules/better-sqlite3/build/Release/obj.target",
			"/node_modules/better-sqlite3/build/Release/sqlite3.a",
			"/node_modules/better-sqlite3/build/Release/test_extension.node",
			"/node_modules/better-sqlite3/build/Makefile",
			"/node_modules/better-sqlite3/binding.gyp",
		]) {
			expect(ignore(file), file).toBe(true);
		}
		expect(ignore("/node_modules/bindings/bindings.js")).toBe(false);
		expect(ignore("/node_modules/file-uri-to-path/index.js")).toBe(false);
		expect(ignore("/node_modules/react/index.js")).toBe(true);
		expect(ignore("/src/main.ts")).toBe(true);
		expect(config.hooks?.prePackage).toBeTypeOf("function");
	});
});

describe("macOS display name", () => {
	const PLIST =
		'<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleDisplayName</key><string>tallpine</string><key>CFBundleExecutable</key><string>tallpine</string></dict></plist>';
	function stagedApp(): { dir: string; plist: string } {
		const dir = mkdtempSync(join(tmpdir(), "forge-display-"));
		const contents = join(dir, "Tallpine.app", "Contents");
		mkdirSync(contents, { recursive: true });
		const plist = join(contents, "Info.plist");
		writeFileSync(plist, PLIST);
		return { dir, plist };
	}

	it("keeps the executable name lowercase", () => {
		expect(config.packagerConfig?.executableName).toBe("tallpine");
	});

	it("leaves the plist alone on non-mac platforms", () => {
		const { dir, plist } = stagedApp();
		try {
			setMacDisplayName(dir, "linux", "Nope");
			expect(readFileSync(plist, "utf8")).toBe(PLIST);
		} finally {
			rmSync(dir, { recursive: true, force: true });
		}
	});

	// Shells out to the real plutil, which only exists on macOS.
	it.skipIf(process.platform !== "darwin")("patches CFBundleDisplayName to the capitalized product name via the packager hook", async () => {
		const { dir, plist } = stagedApp();
		try {
			const hooks = config.packagerConfig?.afterCopyExtraResources as unknown as Array<(a: unknown) => Promise<void>>;
			await hooks[0]({ buildPath: dir, platform: "darwin" });
			const text = readFileSync(plist, "utf8");
			expect(text).toMatch(/<key>CFBundleDisplayName<\/key>\s*<string>Tallpine<\/string>/);
			expect(text).toMatch(/<key>CFBundleExecutable<\/key>\s*<string>tallpine<\/string>/);
		} finally {
			rmSync(dir, { recursive: true, force: true });
		}
	});
});

describe("resolveNotaryProfile", () => {
	it("is undefined when neither variable is set", () => {
		expect(resolveNotaryProfile({})).toBeUndefined();
		expect(resolveNotaryProfile({ TALLPINE_NOTARY_PROFILE: "" })).toBeUndefined();
	});
	it("returns TALLPINE_NOTARY_PROFILE", () => {
		expect(resolveNotaryProfile({ TALLPINE_NOTARY_PROFILE: "new" })).toBe("new");
	});
});
