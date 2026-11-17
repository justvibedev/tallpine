import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import { agentSummary, accessValues } from "./SkillRoleRow";

const t = ((key: string) => key === "settings.instructions.allAgents" ? "All agents" : key === "settings.instructions.noAgents" ? "No agents" : key) as unknown as TFunction;

describe("agentSummary", () => {
	it("says All agents when nothing is switched off", () => {
		expect(agentSummary(accessValues(undefined, () => undefined), t)).toBe("All agents");
	});
	it("lists the agents that are on", () => {
		const access = accessValues(undefined, (key) => (key === "agentKiro" || key === "agentDeepseek" ? false : undefined));
		expect(agentSummary(access, t)).toBe("Claude Code, Codex");
	});
	it("says No agents when all are off", () => {
		expect(agentSummary(accessValues(undefined, (key) => (key.startsWith("agent") ? false : undefined)), t)).toBe("No agents");
	});
});
