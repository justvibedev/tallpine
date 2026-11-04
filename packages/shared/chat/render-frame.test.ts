import { describe, expect, it } from "vitest";
import { clampRenderHeight, readArtifactRef, readRenderRef } from "./render-frame";

describe("shared render-frame readers", () => {
	it("accepts only a well-formed render reference with a render route path", () => {
		const ok = { event: "render" as const, render: { id: "r1", title: "Chart", height: 5000, path: "/api/v1/sessions/p-1/renders/r1" } };
		expect(readRenderRef(ok)).toEqual({ id: "r1", title: "Chart", height: 2000, path: "/api/v1/sessions/p-1/renders/r1" });
		expect(readRenderRef({ event: "render" as const, render: { ...ok.render, path: "https://evil.example/x" } })).toBeUndefined();
		expect(readRenderRef({ event: "steer" as const })).toBeUndefined();
		expect(clampRenderHeight(3)).toBe(80);
	});

	it("accepts only a relative artifact path with an artifact-files URL", () => {
		const artifact = { path: "q3/Q3 (final).html", name: "Q3 (final).html", url: "/api/v1/sessions/p-1/artifact-files/q3/Q3%20%28final%29.html" };
		const read = (fields: Record<string, unknown>) => readArtifactRef({ event: "artifact" as const, artifact: { ...artifact, ...fields } as never });
		expect(read({})).toEqual(artifact);
		for (const bad of [
			{ path: "" },
			{ path: "/etc/report.html" },
			{ path: "\\share\\report.html" },
			{ path: "C:/report.html" },
			{ path: "../report.html" },
			{ path: "q3/../../report.html" },
			{ path: "q3\\..\\report.html" },
			{ path: 7 },
			{ name: undefined },
			{ url: "/api/v1/sessions/p-1/renders/r1" },
			{ url: "/api/v1/sessions/p-1/artifact-files/" },
			{ url: "https://evil.example/api/v1/sessions/p-1/artifact-files/x.html" },
			{ url: "/api/v1/sessions/p-1/artifact-files/../../../config" },
			{ url: "/api/v1/sessions/p-1/artifact-files/%2e%2e/renders/r1" },
			{ url: "/api/v1/sessions/../artifact-files/x.html" },
		]) {
			expect(read(bad), JSON.stringify(bad)).toBeUndefined();
		}
		expect(readArtifactRef({ event: "render" as const, artifact } as never)).toBeUndefined();
		expect(readArtifactRef(undefined)).toBeUndefined();
	});

	it("keeps measured heights only when they are 1-16 positive integer pairs in increasing width", () => {
		const render = { id: "r1", title: "Chart", height: 400, path: "/api/v1/sessions/p-1/renders/r1" };
		const read = (heights: unknown) => readRenderRef({ event: "render" as const, render: { ...render, heights } as never });
		const heights: Array<[number, number]> = [
			[320, 600],
			[640, 300],
		];
		expect(read(heights)).toEqual({ ...render, heights });
		expect(read(undefined)).toEqual(render);
		for (const bad of [
			"320x600",
			[],
			Array.from({ length: 17 }, (_, index) => [320 + index, 100]),
			[[320, 600], [320, 300]],
			[[640, 300], [320, 600]],
			[[320, 0]],
			[[320, 412.5]],
			[[320, Number.POSITIVE_INFINITY]],
			[[320, "600"]],
			[[320, 600, 1]],
			[null],
		]) {
			// The page still shows, at the agent's height.
			expect(read(bad)).toEqual(render);
		}
	});
});
