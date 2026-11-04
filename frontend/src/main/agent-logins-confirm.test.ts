import { describe, expect, it, vi } from "vitest";
import { createAgentLoginsConfirm } from "./agent-logins-confirm";

describe("createAgentLoginsConfirm", () => {
	it("grants only on the explicit button and defaults to decline", async () => {
		const box = vi.fn(async (_options: Parameters<Parameters<typeof createAgentLoginsConfirm>[0]>[0]) => ({ response: 1 }));
		expect(await createAgentLoginsConfirm(box)("worker-42", "Work")).toBe(true);
		const call = box.mock.calls[0]![0];
		expect(call.defaultId).toBe(0);
		expect(call.cancelId).toBe(0);
		expect(call.message).toContain("Work");
		expect(call.message).toContain("worker-42");
		expect(await createAgentLoginsConfirm(async () => ({ response: 0 }))("s", "Work")).toBe(false);
	});
});
