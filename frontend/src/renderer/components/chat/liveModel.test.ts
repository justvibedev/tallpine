import { describe, expect, it } from "vitest";
import type { ChatConfigOption } from "../../types/conversation";
import { liveModel } from "./liveModel";

const option = (currentValue: string): ChatConfigOption => ({
	id: "model", name: "Model", category: "model", type: "select", currentValue,
	choices: [{ value: "opus", name: "Opus 5.5" }],
} as ChatConfigOption);

describe("liveModel", () => {
	it("reads the id and picker name of the running model", () => {
		expect(liveModel("claude-code", [option("opus")])).toEqual({ id: "opus", label: "Opus 5.5" });
	});
	it("is undefined when nothing reports a model", () => {
		expect(liveModel("claude-code", [])).toBeUndefined();
		expect(liveModel("claude-code", [option("default")])).toBeUndefined();
	});
});
