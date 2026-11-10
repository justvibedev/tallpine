import { describe, expect, it } from "vitest";
import { setRoleField, allowedAgents, effortAfterModelChange, effectiveAgent, effortOptions, roleSettingsPatch, roleValuesFromProject } from "./roleSettingsView";

const project = {
	id: "p",
	name: "P",
	config: {
		worker: { agent: "codex", agentConfig: { model: "w1", effort: "high" } },
		reviewers: [{ harness: "claude-code", agentConfig: { model: "r1" } }],
	},
};

describe("roleValuesFromProject", () => {
	it("reads each role and leaves unset fields empty", () => {
		const v = roleValuesFromProject(project);
		expect(v.worker).toEqual({ agent: "codex", model: "w1", effort: "high" });
		expect(v.reviewer).toEqual({ agent: "claude-code", model: "r1", effort: "" });
		expect(v.orchestrator).toEqual({ agent: "", model: "", effort: "" });
		expect(roleValuesFromProject(undefined).worker.agent).toBe("");
	});
});

describe("roleSettingsPatch", () => {
	it("is undefined when nothing changed", () => {
		const v = roleValuesFromProject(project);
		expect(roleSettingsPatch(v, roleValuesFromProject(project))).toBeUndefined();
	});
	it("carries only the changed fields, and an empty string to clear", () => {
		const before = roleValuesFromProject(project);
		const after = roleValuesFromProject(project);
		after.worker.effort = "low";
		after.reviewer.model = "";
		after.orchestrator.agent = "codex";
		expect(roleSettingsPatch(before, after)).toEqual({
			orchestrator: { agent: "codex" },
			worker: { effort: "low" },
			reviewer: { model: "" },
		});
	});
});

describe("effortOptions", () => {
	it("prefers the model's levels, then the agent's, else none", () => {
		expect(effortOptions("codex", ["low", "ultra"])).toEqual(["low", "ultra"]);
		expect(effortOptions("claude-code")).toEqual(["low", "medium", "high", "max"]);
		expect(effortOptions("kiro")).toEqual([]);
	});
});

describe("allowedAgents", () => {
	it("limits the reviewer to the harnesses the daemon accepts", () => {
		expect(allowedAgents("reviewer", ["codex", "cursor", "claude-code", "aider"])).toEqual(["codex", "claude-code"]);
		expect(allowedAgents("worker", ["codex", "cursor"])).toEqual(["codex", "cursor"]);
		expect(allowedAgents("worker", [])).toBeUndefined();
	});
});

describe("effectiveAgent", () => {
	it("lets an unset reviewer follow the worker, not a hardcoded agent", () => {
		const v = roleValuesFromProject({ id: "p", name: "P", config: { worker: { agent: "codex" } } });
		expect(effectiveAgent("reviewer", v)).toBe("codex");
		expect(effectiveAgent("worker", v)).toBe("codex");
		expect(effectiveAgent("orchestrator", v)).toBe("");
	});
});

describe("effortAfterModelChange", () => {
	it("drops an effort the new model does not list", () => {
		expect(effortAfterModelChange("ultra", ["low", "high"])).toBe("");
		expect(effortAfterModelChange("high", ["low", "high"])).toBe("high");
		expect(effortAfterModelChange("high", undefined)).toBe("high");
		expect(effortAfterModelChange("", ["low"])).toBe("");
	});
});

describe("setRoleField", () => {
	it("keeps model and effort when the same agent is chosen again", () => {
		const v = roleValuesFromProject(project);
		expect(setRoleField(v, "worker", "agent", "codex")).toBe(v);
		expect(roleSettingsPatch(v, setRoleField(v, "worker", "agent", "codex"))).toBeUndefined();
	});
	it("resets model and effort when the agent changes, and only then", () => {
		const v = roleValuesFromProject(project);
		expect(setRoleField(v, "worker", "agent", "claude-code").worker).toEqual({ agent: "claude-code", model: "", effort: "" });
		expect(setRoleField(v, "worker", "effort", "low").worker).toEqual({ agent: "codex", model: "w1", effort: "low" });
	});
});
