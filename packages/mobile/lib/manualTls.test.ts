import { describe, expect, it, vi } from "vitest";
import { establishManualTls, ManualTlsDeclinedError } from "./manualTls";

const PIN = "abababababababababababababababababababababababababababababababab";
const cfg = { muxPort: "14801", host: "192.168.1.42", httpPort: "65221", password: "123456" };

describe("establishManualTls", () => {
	it("pins a self-signed gateway only after the user confirms the fingerprint", async () => {
		const register = vi.fn();
		const confirm = vi.fn().mockResolvedValue(true);
		const out = await establishManualTls(cfg, { probe: async () => ({ pin: PIN, trusted: false }), confirm, register });
		expect(confirm).toHaveBeenCalledWith("abab abab abab abab");
		expect(register).toHaveBeenCalledWith([expect.objectContaining({ host: "192.168.1.42", port: 65221, pin: PIN })]);
		expect(out).toMatchObject({ secure: true, pin: PIN });
	});
	it("registers nothing when the user declines", async () => {
		const register = vi.fn();
		await expect(
			establishManualTls(cfg, { probe: async () => ({ pin: PIN, trusted: false }), confirm: async () => false, register }),
		).rejects.toBeInstanceOf(ManualTlsDeclinedError);
		expect(register).not.toHaveBeenCalled();
	});
	it("uses CA validation, no pin and no prompt, for a certificate the system trusts", async () => {
		const confirm = vi.fn();
		const out = await establishManualTls({ ...cfg, host: "mac.tail1.ts.net", httpPort: "443" }, { probe: async () => ({ pin: "", trusted: true }), confirm });
		expect(confirm).not.toHaveBeenCalled();
		expect(out.secure).toBe(true);
		expect(out.pin).toBeUndefined();
	});
	it("drops the old pin and re-confirms when the edited address changed", async () => {
		const confirm = vi.fn().mockResolvedValue(true);
		const out = await establishManualTls(
			{ ...cfg, host: "192.168.1.99", pin: PIN },
			{ probe: async () => ({ pin: PIN.replace(/a/g, "c"), trusted: false }), confirm, register: vi.fn() },
			{ host: "192.168.1.42", port: 65221 },
		);
		expect(confirm).toHaveBeenCalled();
		expect(out.pin).toBe(PIN.replace(/a/g, "c"));
	});
	it("keeps an existing pin without probing", async () => {
		const probe = vi.fn();
		const out = await establishManualTls({ ...cfg, pin: PIN }, { probe, confirm: vi.fn() }, { host: "192.168.1.42", port: 65221 });
		expect(probe).not.toHaveBeenCalled();
		expect(out.pin).toBe(PIN);
	});
});
