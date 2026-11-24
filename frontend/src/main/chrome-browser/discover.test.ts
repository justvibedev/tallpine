import { describe, expect, it } from "vitest";
import { CHROME_CHANNELS, chromeCandidatePaths, findChromeExecutable } from "./discover";

const existing = (...paths: string[]) => (file: string) => paths.includes(file);

describe("findChromeExecutable", () => {
	it("finds Stable at the standard macOS path, then in ~/Applications", () => {
		expect(findChromeExecutable("darwin", { HOME: "/Users/p" }, existing("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")))
			.toEqual({ path: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", channel: "stable" });
		expect(findChromeExecutable("darwin", { HOME: "/Users/p" }, existing("/Users/p/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")))
			?.toMatchObject({ channel: "stable" });
	});

	it("prefers Stable, then Beta, Dev and Canary", () => {
		const mac = (channel: string) => `/Applications/${channel}.app/Contents/MacOS/${channel}`;
		const all = ["Google Chrome Canary", "Google Chrome Dev", "Google Chrome Beta", "Google Chrome"].map(mac);
		expect(CHROME_CHANNELS).toEqual(["stable", "beta", "dev", "canary"]);
		expect(findChromeExecutable("darwin", {}, existing(...all))?.channel).toBe("stable");
		expect(findChromeExecutable("darwin", {}, existing(...all.slice(0, 3)))?.channel).toBe("beta");
		expect(findChromeExecutable("darwin", {}, existing(...all.slice(0, 2)))?.channel).toBe("dev");
		expect(findChromeExecutable("darwin", {}, existing(all[0]))).toEqual({ path: all[0], channel: "canary" });
	});

	it("uses Program Files and LocalAppData on Windows, case-insensitively", () => {
		const env = { ProgramFiles: "C:\\Program Files", "ProgramFiles(x86)": "C:\\Program Files (x86)", LocalAppData: "C:\\Users\\p\\AppData\\Local" };
		expect(chromeCandidatePaths("win32", "stable", env)).toEqual([
			"C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
			"C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe",
			"C:\\Users\\p\\AppData\\Local\\Google\\Chrome\\Application\\chrome.exe",
		]);
		expect(chromeCandidatePaths("win32", "canary", env)).toEqual(["C:\\Users\\p\\AppData\\Local\\Google\\Chrome SxS\\Application\\chrome.exe"]);
		expect(findChromeExecutable("win32", env, existing("C:\\Users\\p\\AppData\\Local\\Google\\Chrome Beta\\Application\\chrome.exe"))?.channel).toBe("beta");
	});

	it("uses the Google packages' paths on Linux", () => {
		expect(chromeCandidatePaths("linux", "stable", {})[0]).toBe("/opt/google/chrome/chrome");
		expect(findChromeExecutable("linux", {}, existing("/usr/bin/google-chrome-unstable"))).toEqual({ path: "/usr/bin/google-chrome-unstable", channel: "dev" });
	});

	it("returns null when no channel is installed (never a download)", () => {
		for (const platform of ["darwin", "win32", "linux"] as const) {
			expect(findChromeExecutable(platform, { HOME: "/h", PROGRAMFILES: "C:\\P", LOCALAPPDATA: "C:\\L" }, () => false)).toBeNull();
		}
	});
});
