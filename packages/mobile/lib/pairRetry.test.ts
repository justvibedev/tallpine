import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({
	default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() },
}));

import type { MachineAppearance } from "./machineAppearance";
import { classifyConnectionFailure } from "./connectionError";
import type { Endpoint } from "./endpoints";
import { exchangeForDeviceToken } from "./pairExchange";
import { pairFromCode, resetIssuedTokens } from "./pairFlow";
import { encodePairingCode } from "./pairingCode";

const lan: Endpoint = { kind: "lan", host: "192.168.1.42", port: 3011, secure: true };
const code = encodePairingCode({ v: 2, hostId: "h_paired", name: "mac", platform: "darwin", endpoints: [lan], token: "Code1234" });
const link = `tallpine-mobile://pair#${code}`;

const deps = (over: Record<string, unknown> = {}) => ({
	race: vi.fn(async () => ({ ok: true as const, endpoint: lan, hostId: "h_paired" })),
	exchange: vi.fn(async () => ({ ok: true as const, token: "vot_issued" })),
	verify: vi.fn(async () => {}),
	chooseName: vi.fn(async (s: string, a: MachineAppearance) => ({ name: s, appearance: a })),
	saveHost: vi.fn(async () => {}),
	setActiveHost: vi.fn(async () => {}),
	...over,
});

beforeEach(() => resetIssuedTokens());

describe("retrying a pairing whose verify step failed", () => {
	it("reuses the token the first exchange issued instead of redeeming the spent code", async () => {
		const verify = vi.fn().mockRejectedValueOnce(Object.assign(new Error("timeout"), { status: undefined })).mockResolvedValue(undefined);
		const d = deps({ verify });

		const first = await pairFromCode(link, d);
		expect(first).toMatchObject({ ok: false, reason: "verify-failed" });

		const second = await pairFromCode(link, d);
		expect(second.ok).toBe(true);
		expect(d.exchange).toHaveBeenCalledTimes(1);
		if (second.ok) expect(second.config.password).toBe("vot_issued");
	});

	it("forgets the token once the pairing is saved", async () => {
		const d = deps();
		await pairFromCode(link, d);
		await pairFromCode(link, d);
		expect(d.exchange).toHaveBeenCalledTimes(2);
	});
});

describe("an already-used pairing code", () => {
	it("reports the daemon's code, which reads as a used code and not a rotated password", async () => {
		const d = deps({ exchange: vi.fn(async () => ({ ok: false as const, status: 401, code: "PAIRING_CODE_USED" })) });
		const result = await pairFromCode(link, d);
		expect(result).toMatchObject({ ok: false, reason: "verify-failed", status: 401, code: "PAIRING_CODE_USED" });
		if (!result.ok && result.reason === "verify-failed") {
			expect(classifyConnectionFailure(result.status, result.code)).toBe("code-used");
		}
	});

	it("exchange surfaces the envelope code from the daemon's refusal", async () => {
		const res = { ok: false, status: 401, json: async () => ({ code: "PAIRING_CODE_USED" }) };
		const got = await exchangeForDeviceToken(lan, { code: "Code1234" }, { fetch: async () => res, installId: async () => "i-1" });
		expect(got).toEqual({ ok: false, status: 401, code: "PAIRING_CODE_USED" });
	});
});
