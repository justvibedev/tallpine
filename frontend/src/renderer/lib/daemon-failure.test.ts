import { describe, expect, it } from "vitest";
import { daemonFailureHint, daemonFailureTitle } from "./daemon-failure";

describe("daemonFailureTitle", () => {
	it.each([
		{ code: "not_ready" as const, title: "Tallpine daemon is not ready yet" },
		{ code: "port_unconfirmed" as const, title: "Tallpine daemon is not ready yet" },
		{ code: "not_configured" as const, title: "Tallpine daemon is not configured" },
		{ code: "daemon_unreachable" as const, title: "Tallpine daemon is unreachable" },
		{ code: "identity_mismatch" as const, title: "Tallpine daemon identity check failed" },
		{ code: "binary_missing" as const, title: "Tallpine daemon binary is missing" },
		{ code: "signature_invalid" as const, title: "Tallpine helper failed its code-signature check" },
		{ code: "spawn_failed" as const, title: "Tallpine daemon failed to start" },
		{ code: "exited" as const, title: "Tallpine daemon failed to start" },
	])("returns $title for $code", ({ code, title }) => {
		expect(daemonFailureTitle({ state: "error", code })).toBe(title);
	});
});

describe("daemonFailureHint", () => {
	it("tells the user to reinstall on signature_invalid", () => {
		expect(daemonFailureHint({ state: "error", code: "signature_invalid" })).toContain("Reinstall");
	});
});
