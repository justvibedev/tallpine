// @vitest-environment node
import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import {
	decideRelocation,
	installedBundlePath,
	isDevOrTestLaunch,
	readBundleVersion,
	type RelocationInputs,
} from "./relocation";

const BASE: RelocationInputs = {
	inApplicationsFolder: false,
	installedPresent: true,
	installedVersion: "0.11.2-nightly.202608050602",
	runningVersion: "0.10.3",
};

describe("decideRelocation", () => {
	// The reported bug: a stale 0.10.3 download launching while /Applications
	// holds a newer nightly. This MUST NOT relocate, or the nightly is trashed.
	it("hands off when the install is newer", () => {
		expect(decideRelocation(BASE)).toBe("handoff");
	});

	it("hands off when the install is the same version", () => {
		expect(
			decideRelocation({ ...BASE, installedVersion: "0.10.3" }),
		).toBe("handoff");
	});

	it("relocates when /Applications is empty", () => {
		expect(
			decideRelocation({
				...BASE,
				installedPresent: false,
				installedVersion: null,
			}),
		).toBe("relocate");
	});

	it("relocates over a strictly older install", () => {
		expect(
			decideRelocation({
				...BASE,
				installedVersion: "0.10.3",
				runningVersion: "0.12.0",
			}),
		).toBe("relocate");
	});

	it("treats a nightly as older than the stable it precedes", () => {
		expect(
			decideRelocation({
				...BASE,
				installedVersion: "0.11.2-nightly.202608050602",
				runningVersion: "0.11.2",
			}),
		).toBe("relocate");
	});

	it("stays put when already in an Applications folder", () => {
		expect(
			decideRelocation({ ...BASE, inApplicationsFolder: true }),
		).toBe("stay");
	});

	// Never destroy an install we could not identify, and never hand the
	// session to a bundle we could not identify.
	it("stays put when the installed version is unreadable", () => {
		expect(decideRelocation({ ...BASE, installedVersion: null })).toBe("stay");
	});

	it("stays put when either version is not semver", () => {
		expect(
			decideRelocation({ ...BASE, installedVersion: "garbage" }),
		).toBe("stay");
		expect(
			decideRelocation({ ...BASE, runningVersion: "garbage" }),
		).toBe("stay");
	});
});

describe("installedBundlePath", () => {
	it("maps any running bundle to the same name under /Applications", () => {
		expect(
			installedBundlePath("/Users/x/Downloads/Tallpine.app"),
		).toBe("/Applications/Tallpine.app");
		expect(
			installedBundlePath("/Volumes/Tallpine/Tallpine.app"),
		).toBe("/Applications/Tallpine.app");
	});
});

describe("readBundleVersion", () => {
	let dir: string;
	let bundle: string;

	beforeEach(async () => {
		dir = await mkdtemp(path.join(os.tmpdir(), "tallpine-relocation-"));
		bundle = path.join(dir, "Tallpine.app");
		await mkdir(path.join(bundle, "Contents"), { recursive: true });
	});

	afterEach(async () => {
		await rm(dir, { recursive: true, force: true });
	});

	async function writePlist(body: string): Promise<void> {
		await writeFile(path.join(bundle, "Contents", "Info.plist"), body, "utf8");
	}

	// Shaped like a real Forge-written Info.plist: XML, other keys around it.
	it("reads the short version string out of an XML plist", async () => {
		await writePlist(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>CFBundleDisplayName</key>
    <string>Tallpine</string>
    <key>CFBundleShortVersionString</key>
    <string>0.11.2-nightly.202608050602</string>
    <key>CFBundleVersion</key>
    <string>0.11.2-nightly.202608050602</string>
  </dict>
</plist>
`);
		expect(readBundleVersion(bundle)).toBe("0.11.2-nightly.202608050602");
	});

	it("returns null when the key is missing", async () => {
		await writePlist("<plist><dict></dict></plist>");
		expect(readBundleVersion(bundle)).toBeNull();
	});

	it("returns null when the bundle does not exist", () => {
		expect(readBundleVersion(path.join(dir, "Nope.app"))).toBeNull();
	});
});

describe("decideRelocation dev/test launches", () => {
	it("stays for a dev/test launch even with an empty /Applications", () => {
		expect(
			decideRelocation({
				...BASE,
				installedPresent: false,
				installedVersion: null,
				devOrTestLaunch: true,
			}),
		).toBe("stay");
	});

	it("stays for a dev/test launch instead of handing off", () => {
		expect(decideRelocation({ ...BASE, devOrTestLaunch: true })).toBe("stay");
	});

	it("still relocates a normal end-user launch", () => {
		expect(
			decideRelocation({
				...BASE,
				installedPresent: false,
				installedVersion: null,
				devOrTestLaunch: false,
			}),
		).toBe("relocate");
	});
});

describe("isDevOrTestLaunch", () => {
	const home = "/Users/u";
	const DL = "/Users/u/Downloads/Tallpine.app";
	const ctx = (env: NodeJS.ProcessEnv, bundlePath = DL) => ({
		env,
		homeDir: home,
		bundlePath,
	});

	it("is false for a normal end user", () => {
		expect(isDevOrTestLaunch(ctx({}))).toBe(false);
		expect(
			isDevOrTestLaunch(ctx({}, "/Volumes/Vibe/Tallpine.app")),
		).toBe(false);
	});

	it("is false when env holds the default locations", () => {
		expect(
			isDevOrTestLaunch(
				ctx({
					TALLPINE_DATA_DIR: "/Users/u/.tallpine/data",
					TALLPINE_RUN_FILE: "/Users/u/.tallpine/running.json",
				}),
			),
		).toBe(false);
	});

	it("honors TALLPINE_DISABLE_RELOCATION=1 only", () => {
		expect(isDevOrTestLaunch(ctx({ TALLPINE_DISABLE_RELOCATION: "1" }))).toBe(true);
		expect(isDevOrTestLaunch(ctx({ TALLPINE_DISABLE_RELOCATION: "0" }))).toBe(false);
	});

	it("is true for a non-default TALLPINE_DATA_DIR", () => {
		expect(isDevOrTestLaunch(ctx({ TALLPINE_DATA_DIR: "/tmp/scratch/data" }))).toBe(
			true,
		);
	});

	it("is true for a non-default TALLPINE_RUN_FILE", () => {
		expect(
			isDevOrTestLaunch(ctx({ TALLPINE_RUN_FILE: "/tmp/scratch/run.json" })),
		).toBe(true);
	});

	it("is true for a bundle inside ~/.tallpine (worktrees, data dir)", () => {
		expect(
			isDevOrTestLaunch(ctx({}, "/Users/u/.tallpine/data/worktrees/p/s/V.app")),
		).toBe(true);
	});

	it("is true for a bundle inside TALLPINE_DATA_DIR", () => {
		expect(
			isDevOrTestLaunch(
				ctx({ TALLPINE_DATA_DIR: "/Users/u/.tallpine/data" }, "/Users/u/.tallpine/data/x/V.app"),
			),
		).toBe(true);
	});

	it("is true for a bundle in a frontend/out build folder", () => {
		expect(
			isDevOrTestLaunch(ctx({}, "/Users/u/code/aox/frontend/out/V-arm64/V.app")),
		).toBe(true);
	});
});
