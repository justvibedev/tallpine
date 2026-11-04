import { describe, expect, it } from "vitest";
import { parseChangeRequestUrl } from "./change-request-url";

describe("parseChangeRequestUrl", () => {
	it.each([
		["https://github.com/a/b/pull/7/files?x=1#y", "github", "https://github.com/a/b/pull/7", "a/b", 7],
		["https://acme.ghe.com/a/b/pull/8", "github", "https://acme.ghe.com/a/b/pull/8", "a/b", 8],
		["https://ghes.corp.example:8443/a/b/pull/9", "github", "https://ghes.corp.example:8443/a/b/pull/9", "a/b", 9],
		["https://gitlab.com/g/s/p/-/merge_requests/3/diffs", "gitlab", "https://gitlab.com/g/s/p/-/merge_requests/3", "g/s/p", 3],
		["https://git.corp.example/a/b/-/merge_requests/4", "gitlab", "https://git.corp.example/a/b/-/merge_requests/4", "a/b", 4],
	])("%s", (raw, provider, url, repository, number) => {
		expect(parseChangeRequestUrl(raw)).toEqual({ provider, url, repository, number });
	});
	it("rejects non-PR URLs", () => {
		expect(parseChangeRequestUrl("https://github.com/a/b")).toBeUndefined();
		expect(parseChangeRequestUrl("https://github.com/a/b/blob/main/pull/3")).toBeUndefined();
		expect(parseChangeRequestUrl("javascript:alert(1)")).toBeUndefined();
		expect(parseChangeRequestUrl("")).toBeUndefined();
	});
});
