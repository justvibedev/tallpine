import { describe, expect, it } from "vitest";
import { shellNavSection } from "./shell-nav-sections";

describe("shellNavSection", () => {
	it.each([
		["/", "home"],
		["/automations", "automations"],
		["/artifacts", "artifacts"],
		["/backlog", "backlog"],
		["/inbox", "inbox"],
		["/terminals", "terminals"],
		["/sessions", "sessions"],
		["/sessions/s1", "sessions"],
		["/projects/p1", "sessions"],
		["/projects/p1/sessions/s1", "sessions"],
		["/host/h1/project/p1", "sessions"],
		["/artifactsfoo", "sessions"],
	])("maps %s to %s", (pathname, section) => {
		expect(shellNavSection(pathname)).toBe(section);
	});
});
