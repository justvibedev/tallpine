import { describe, expect, it } from "vitest";
import managedKeys from "./managed-keys.json";
import {
	PROJECT_LOCK_KEY,
	PROJECT_MANAGED_KEYS,
	lockedEchoPatch,
	lockedEchoFields,
	managedProjectKeyNames,
} from "./project-managed-keys";

describe("project managed keys table", () => {
	it("has a row for every project.* key in managed-keys.json, and no stale rows", () => {
		const names = managedProjectKeyNames().sort();
		expect(names.length).toBeGreaterThan(0);
		expect(PROJECT_MANAGED_KEYS.map((row) => row.key).sort()).toEqual(names);
		expect(names.every((name) => managedKeys.some((entry) => entry.key === name))).toBe(true);
	});

	it("covers the keys the editor locks sections with", () => {
		for (const key of Object.values(PROJECT_LOCK_KEY)) {
			expect(PROJECT_MANAGED_KEYS.some((row) => row.key === key), key).toBe(true);
		}
	});

	it("echoes agentConfig when either role is locked, and nothing when nothing is locked", () => {
		expect([...lockedEchoFields(new Set())]).toEqual([]);
		expect(lockedEchoFields(new Set(["project.worker"]))).toEqual(new Set(["worker", "agentConfig"]));
		expect(lockedEchoFields(new Set(["project.orchestrator"]))).toEqual(new Set(["orchestrator", "agentConfig"]));
	});

	it("does not echo a locked field the draft rebuilt, and leaves unlocked fields alone", () => {
		const loaded = { worker: { agent: "codex" }, orchestrator: { agent: "claude" }, reviewers: [{ harness: "a" }], agentConfig: { model: "m" } };
		const draft = { worker: { agent: "edited" }, orchestrator: { agent: "edited" }, reviewers: [{ harness: "edited" }], agentConfig: { model: "edited" } };
		const sent = { ...draft, ...lockedEchoPatch(loaded, new Set(["project.worker"])) };
		expect(sent.worker).toBe(loaded.worker);
		expect(sent.agentConfig).toBe(loaded.agentConfig);
		expect(sent.orchestrator).toBe(draft.orchestrator);
		expect(sent.reviewers).toBe(draft.reviewers);
	});
});
