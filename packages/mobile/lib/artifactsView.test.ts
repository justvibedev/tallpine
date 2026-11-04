import { describe, expect, it } from "vitest";
import type { SessionArtifact } from "./api";
import { artifactIcon, artifactSizeLabel, sortArtifacts } from "./artifactsView";

const file = (path: string, updatedAt: string): SessionArtifact => ({ path, name: path, kind: "file", size: 1, updatedAt });

describe("sortArtifacts", () => {
	it("puts the newest file first and ties break by path", () => {
		const sorted = sortArtifacts([file("b", "2026-10-30T10:00:00Z"), file("c", "2026-10-30T12:00:00Z"), file("a", "2026-10-30T10:00:00Z")]);
		expect(sorted.map((f) => f.path)).toEqual(["c", "a", "b"]);
	});
	it("does not mutate its input", () => {
		const input = [file("a", "1"), file("b", "2")];
		sortArtifacts(input);
		expect(input.map((f) => f.path)).toEqual(["a", "b"]);
	});
});

describe("artifactSizeLabel", () => {
	it("scales bytes, KB and MB", () => {
		expect(artifactSizeLabel(512)).toBe("512 B");
		expect(artifactSizeLabel(2048)).toBe("2 KB");
		expect(artifactSizeLabel(3 * 1024 * 1024)).toBe("3.0 MB");
	});
});

describe("artifactIcon", () => {
	it("maps kinds", () => {
		expect(artifactIcon("html")).toBe("globe");
		expect(artifactIcon("markdown")).toBe("file-text");
		expect(artifactIcon("file")).toBe("file");
	});
});
