import { describe, expect, it } from "vitest";
import { groupPendingPaths, projectRelativeTarget } from "./skills-offer";

describe("skills offer display helpers", () => {
	it("resolves a link target against the link's folder so no ../ is shown", () => {
		expect(projectRelativeTarget(".claude/skills/graphify", "../../.agents/skills/graphify")).toBe(".agents/skills/graphify");
		expect(projectRelativeTarget("AGENTS.md", "CLAUDE.md")).toBe("CLAUDE.md");
		expect(projectRelativeTarget("a/b/c", "./d")).toBe("a/b/d");
	});
	it("groups pending paths: instruction files, one entry per skill folder, the rest raw", () => {
		expect(groupPendingPaths(["CLAUDE.md", ".claude/skills/a", ".claude/skills/a/SKILL.md", ".claude/skills/b", "docs/x.md"])).toEqual({ instructions: ["CLAUDE.md"], skills: ["a", "b"], other: ["docs/x.md"] });
	});
});
