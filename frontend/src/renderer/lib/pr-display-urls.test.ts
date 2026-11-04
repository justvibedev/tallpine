import { describe, expect, it } from "vitest";
import { prChecksUrl, prRepositoryFromUrl } from "./pr-display";

describe("PR URL helpers on GHE and GitLab", () => {
	it("derives the repository beyond github.com", () => {
		expect(prRepositoryFromUrl("https://acme.ghe.com/o/r/pull/3/files")).toBe("o/r");
		expect(prRepositoryFromUrl("https://gl.example/g/s/p/-/merge_requests/3")).toBe("g/s/p");
	});
});

describe("prChecksUrl", () => {
	const pr = (url: string) => ({ htmlUrl: url, url, mergeability: {} }) as never;
	it("links checks for GHE.com but not GitLab", () => {
		expect(prChecksUrl(pr("https://acme.ghe.com/o/r/pull/3"))).toBe("https://acme.ghe.com/o/r/pull/3/checks");
		expect(prChecksUrl(pr("https://gitlab.com/g/p/-/merge_requests/3"))).toBeUndefined();
	});
});
