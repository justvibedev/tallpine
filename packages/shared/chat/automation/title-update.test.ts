import { describe, expect, it } from "vitest";
import { isTaskTitleUpdate, renamedTitle } from "./title-update";

const prompt = { kind: "message", role: "user", origin: "automation", text: "Tallpine TASK TITLE UPDATE\nName it." };
const command = (c: string) => ({ kind: "activity", activityKind: "command", summary: "", detail: { command: c } });

describe("task title updates", () => {
	it("recognises the naming prompt only from automation", () => {
		expect(isTaskTitleUpdate(prompt)).toBe(true);
		expect(isTaskTitleUpdate({ ...prompt, origin: "human" })).toBe(false);
		expect(isTaskTitleUpdate({ ...prompt, role: "assistant" })).toBe(false);
	});
	it("reads the rename from tallpine or tp, quoted either way, else the summary", () => {
		expect(renamedTitle([command(`tallpine session rename app-1 "Fix login"`)])).toBe("Fix login");
		expect(renamedTitle([command(`tp' session rename app-1 'Docs pass'`)])).toBe("Docs pass");
		expect(renamedTitle([{ kind: "activity", activityKind: "command", summary: `tallpine session rename a "X"` }])).toBe("X");
		expect(renamedTitle([command("ls"), { kind: "message" }])).toBeUndefined();
	});
});
