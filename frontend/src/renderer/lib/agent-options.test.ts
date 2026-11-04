import { AGENT_OPTIONS, agentLabel } from "./agent-options";

describe("AGENT_OPTIONS", () => {
	it("offers only the supported harnesses, each exactly once", () => {
		expect([...AGENT_OPTIONS]).toEqual(["claude-code", "codex", "kiro", "deepseek-harness"]);
		expect(new Set(AGENT_OPTIONS).size).toBe(AGENT_OPTIONS.length);
	});

	it("labels supported agents and keeps the raw id for a removed one", () => {
		expect(agentLabel("kiro")).toBe("Kiro");
		expect(agentLabel("opencode")).toBe("opencode");
	});
});
