import { describe, expect, it } from "vitest";
import { findLocalImagePaths, findLocalMediaPaths, isLocalImagePath, isLocalMediaPath, MAX_LOCAL_IMAGE_PATHS } from "./local-image-paths";

const SHOT = "/Users/me/.tallpine/data/artifacts/ranker-6/a-light-editorial.png";

describe("findLocalImagePaths", () => {
	it("finds image paths in inline code, links and bare prose, in order", () => {
		const text = [
			`Saved \`${SHOT}\`.`,
			"Also [the dark one](/Users/me/.tallpine/data/artifacts/ranker-6/dark%20mode.jpg) and",
			"/Users/me/work/s-1/docs/b.webp, plus [file](file:///tmp/x/c.GIF) and [spaced](</tmp/x/d e.jpeg>).",
		].join("\n");
		expect(findLocalImagePaths(text)).toEqual([
			SHOT,
			"/Users/me/.tallpine/data/artifacts/ranker-6/dark mode.jpg",
			"/Users/me/work/s-1/docs/b.webp",
			"/tmp/x/c.GIF",
			"/tmp/x/d e.jpeg",
		]);
	});

	it("ignores non-images, URLs, relative and home paths, fenced code, and duplicates", () => {
		const text = [
			"See /Users/me/a.txt, https://x.dev/a/b.png, docs/c.png, ~/d/e.png and /Users/me/f.svg",
			`\`${SHOT}\` again: ${SHOT}`,
			"```",
			"/Users/me/in-fence.png",
			"```",
		].join("\n");
		expect(findLocalImagePaths(text)).toEqual([SHOT]);
	});

	it("caps how many it returns", () => {
		const text = Array.from({ length: 20 }, (_, index) => `/tmp/x/${index}.png`).join(" ");
		expect(findLocalImagePaths(text)).toHaveLength(MAX_LOCAL_IMAGE_PATHS);
	});

	it("rejects parent traversal", () => {
		expect(isLocalImagePath("/Users/me/.tallpine/data/artifacts/s/../../secret.png")).toBe(false);
		expect(findLocalImagePaths("`/a/artifacts/s/../../b.png`")).toEqual([]);
	});
});

describe("findLocalMediaPaths", () => {
	it("finds video and audio paths but not images, URLs or traversal", () => {
		const text = "Watch `/a/artifacts/s/run.mp4` and [clip](/a/b/c%20d.webm), listen /a/take.WAV; /a/x.png https://x.dev/y.mp4 /a/../b.mp3";
		expect(findLocalMediaPaths(text)).toEqual(["/a/artifacts/s/run.mp4", "/a/b/c d.webm", "/a/take.WAV"]);
		expect(isLocalMediaPath("/a/b.png")).toBe(false);
	});
});
