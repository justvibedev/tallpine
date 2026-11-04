import { describe, expect, it } from "vitest";
import { AGENT_OPTIONS, agentLabel, getAgentIdentity } from "./agents";

describe("agent identities", () => {
	it("offers only the supported worker agents", () => {
		expect([...AGENT_OPTIONS]).toEqual(["claude-code", "codex", "kiro", "deepseek-harness"]);
		expect(agentLabel("kiro")).toBe("Kiro");
		expect(getAgentIdentity("kiro")).toMatchObject({ id: "kiro", label: "Kiro", logoKey: "kiro" });
	});

	it("still names a session saved under a removed agent", () => {
		expect(agentLabel("opencode")).toBe("opencode");
		expect(getAgentIdentity("opencode")).toMatchObject({ id: "opencode", label: "opencode", initial: "O" });
	});
});
