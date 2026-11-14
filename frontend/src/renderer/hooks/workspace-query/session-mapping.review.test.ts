import { describe, expect, it } from "vitest";
import type { components } from "../../../api/schema";
import { toWorkspaceSession } from "./session-mapping";

const project = { id: "p1", name: "proj" };

function view(extra: Partial<components["schemas"]["SessionView"]>): components["schemas"]["SessionView"] {
	return { id: "s1", status: "idle", statusReadiness: "ready", ...extra } as components["schemas"]["SessionView"];
}

describe("toWorkspaceSession review and land facts", () => {
	it("carries the daemon's review verdict and landed flag", () => {
		const session = toWorkspaceSession(view({ reviewVerdict: "approved", landed: true }), project);
		expect(session.reviewVerdict).toBe("approved");
		expect(session.landed).toBe(true);
	});

	it("leaves them unset when the daemon sends none", () => {
		const session = toWorkspaceSession(view({}), project);
		expect(session.reviewVerdict).toBeUndefined();
		expect(session.landed).toBeUndefined();
	});
});
