import { describe, expect, it } from "vitest";
import { findLocalPaths, isLocalPathCandidate } from "./local-path";

const paths = (text: string) => findLocalPaths(text).map((m) => m.path);

describe("findLocalPaths", () => {
	it("finds absolute and home paths in prose", () => {
		expect(paths("See /Users/me/proj/file.ts and ~/Downloads/a.png now")).toEqual(["/Users/me/proj/file.ts", "~/Downloads/a.png"]);
	});
	it("trims trailing punctuation and line suffixes", () => {
		expect(paths("Edit (/tmp/a/b.go:12:3), then /var/log/x.log.")).toEqual(["/tmp/a/b.go", "/var/log/x.log"]);
	});
	it("reports offsets", () => {
		const [match] = findLocalPaths("at /a/b.");
		expect(match).toEqual({ start: 3, end: 7, path: "/a/b" });
	});
	it("ignores URLs, ratios, dates and words", () => {
		expect(paths("https://example.com/a/b and http://x/y/z")).toEqual([]);
		expect(paths("and/or, 1/2, 3/4/5, w/o")).toEqual([]);
		expect(paths("on 2024/10/25 or /2024/10/25")).toEqual([]);
		expect(paths("//cdn.example.com/a/b file:///a/b tallpine://sessions/p/s")).toEqual([]);
		expect(paths("a/b/c src/main.ts ./x/y ../x/y")).toEqual([]);
		expect(paths("use /tmp alone or ~ alone or ~user/x/y")).toEqual([]);
	});
});

describe("findLocalPaths with spaces", () => {
	const f = "/Users/me/dist/Sample App-0.1.0-arm64.dmg";
	it("links a spaced file name whole", () => {
		expect(paths(`Built ${f} today`)).toEqual([f]);
		expect(paths(`(${f}).`)).toEqual([f]);
		expect(paths(`${f}, then more`)).toEqual([f]);
	});
	it("does not swallow prose after a path", () => {
		expect(paths("see /Users/me/dist and then run it")).toEqual(["/Users/me/dist"]);
		expect(paths("/a/b/c.txt is ready")).toEqual(["/a/b/c.txt"]);
		expect(paths("/a/b/c d e f g h i.txt")).toEqual(["/a/b/c"]);
	});
	it("keeps prose-then-filename out of the link", () => {
		expect(paths("Wrote /tmp/out.log and config.yaml")).toEqual(["/tmp/out.log"]);
		expect(paths("Edit /src/app and update config.json")).toEqual(["/src/app"]);
		expect(paths("cd /Users/me/app then bump to 1.2")).toEqual(["/Users/me/app"]);
		expect(paths("/tmp/a.txt and b.txt")).toEqual(["/tmp/a.txt"]);
		expect(paths("/Users/me/proj and open README.md")).toEqual(["/Users/me/proj"]);
		expect(paths("/x/y then 1.2")).toEqual(["/x/y"]);
		expect(paths("/x/y Then 1.2")).toEqual(["/x/y"]);
	});
	it("handles extensions ending in a digit", () => {
		expect(paths("/Users/me/Movies/Family Trip.mp4 ok")).toEqual(["/Users/me/Movies/Family Trip.mp4"]);
		expect(paths("/Users/me/Music/Best Of.mp3.")).toEqual(["/Users/me/Music/Best Of.mp3"]);
	});
	it("still finds later paths", () => {
		expect(paths(`${f} and /tmp/x/y`)).toEqual([f, "/tmp/x/y"]);
	});
});

describe("isLocalPathCandidate", () => {
	it("accepts whole-string paths", () => {
		expect(isLocalPathCandidate("/Users/me/a b/c.txt")).toBe(true);
		expect(isLocalPathCandidate("~/x")).toBe(true);
	});
	it("rejects non-paths", () => {
		for (const v of ["", "~/", "/tmp", "//host/a/b", "file:///a/b", "C:/a/b", " /a/b", "/a/b\n", "src/a/b", "/1/2/3"]) {
			expect(isLocalPathCandidate(v), v).toBe(false);
		}
	});
});
