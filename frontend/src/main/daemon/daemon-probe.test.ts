import { describe, expect, it } from "vitest";
import { daemonIdentityError, processAlive, readDaemonProbe } from "./daemon-probe";

describe("daemon probe", () => {
	it("processAlive is false for pid 0 and true for this process", () => {
		expect(processAlive(0)).toBe(false);
		expect(processAlive(process.pid)).toBe(true);
	});

	it("readDaemonProbe returns null for a non-ok response and on network failure", async () => {
		const notOk = (async () => ({ ok: false })) as unknown as typeof fetch;
		const boom = (async () => {
			throw new Error("refused");
		}) as unknown as typeof fetch;
		expect(await readDaemonProbe(1, "healthz", notOk)).toBeNull();
		expect(await readDaemonProbe(1, "healthz", boom)).toBeNull();
	});

	it("flags a dev daemon that does not report its checkout identity", () => {
		const launch = { source: "dev", cwd: "/repo" } as Parameters<typeof daemonIdentityError>[0];
		const error = daemonIdentityError(launch, {} as Parameters<typeof daemonIdentityError>[1], "Tallpine", undefined);
		expect(error).toContain("does not report its checkout identity");
	});
});
