import { describe, expect, it, vi } from "vitest";

const setPins = vi.fn();
(globalThis as { expo?: unknown }).expo = { modules: { TallpinePinning: { setPins, probe: vi.fn() } } };

vi.mock("@react-native-async-storage/async-storage", () => ({
	default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() },
}));

import type { Endpoint } from "./endpoints";
import { machineAppearance, type MachineAppearance } from "./machineAppearance";
import { type MachineNameChoice, pairFromCode } from "./pairFlow";
import { encodePairingCode, parsePairingCode } from "./pairingCode";
import { registerPins, resetPinRegistrationForTest } from "./pinning";

const lan: Endpoint = { kind: "lan", host: "192.168.1.42", port: 3011, secure: true };
const tunnel: Endpoint = { kind: "tunnel", host: "abc.trycloudflare.com", port: 443, secure: true };

const code = encodePairingCode({
	v: 2,
	hostId: "h_paired",
	name: "dev-mbp",
	platform: "darwin",
	endpoints: [lan, tunnel],
	token: "pw",
});

const deps = (over: Record<string, unknown> = {}) => ({
	race: vi.fn(async () => ({ ok: true as const, endpoint: lan, hostId: "h_paired" })),
	verify: vi.fn(async () => {}),
	chooseName: vi.fn(async (suggested: string, appearance: MachineAppearance): Promise<MachineNameChoice | null> => ({ name: suggested, appearance })),
	saveHost: vi.fn(async () => {}),
	setActiveHost: vi.fn(async () => {}),
	...over,
});

describe("pairFromCode", () => {
	it("requires a confirmed human-readable name before saving a new computer", async () => {
		const d = deps({ chooseName: vi.fn(async () => ({ name: " Studio Mac ", appearance: { symbol: "laptop" as const } })) });

		await pairFromCode(`tallpine-mobile://pair#${code}`, d);

		expect(d.chooseName).toHaveBeenCalledWith("dev-mbp", machineAppearance({ id: "h_paired", name: "dev-mbp" }), "h_paired");
		expect(d.saveHost).toHaveBeenCalledWith(expect.objectContaining({ name: "Studio Mac", appearance: { symbol: "laptop" } }));
	});

	it("stores nothing when computer naming is cancelled", async () => {
		const d = deps({ chooseName: vi.fn(async () => null) });

		const got = await pairFromCode(`tallpine-mobile://pair#${code}`, d);

		expect(got).toEqual({ ok: false, reason: "cancelled" });
		expect(d.saveHost).not.toHaveBeenCalled();
		expect(d.setActiveHost).not.toHaveBeenCalled();
	});

	it("preserves the saved name when repairing the expected computer", async () => {
		const d = deps();

		await pairFromCode(`tallpine-mobile://pair#${code}`, d, { repairHost: { id: "h_paired", name: "Prod" } });

		expect(d.chooseName).not.toHaveBeenCalled();
		expect(d.saveHost).toHaveBeenCalledWith(expect.objectContaining({ id: "h_paired", name: "Prod", token: "pw" }));
	});

	it("preserves the local appearance when repairing the expected computer", async () => {
		const d = deps();
		const appearance = { symbol: "laptop" as const };
		await pairFromCode(`tallpine-mobile://pair#${code}`, d, { repairHost: { id: "h_paired", name: "Prod", appearance } });
		expect(d.saveHost).toHaveBeenCalledWith(expect.objectContaining({ appearance }));
	});

	it("rejects a different computer before probing during repair", async () => {
		const d = deps();

		const got = await pairFromCode(`tallpine-mobile://pair#${code}`, d, { repairHost: { id: "h_other", name: "Dev" } });

		expect(got).toEqual({ ok: false, reason: "wrong-machine" });
		expect(d.race).not.toHaveBeenCalled();
		expect(d.saveHost).not.toHaveBeenCalled();
	});

	it("stores the computer with every endpoint the code carried", async () => {
		const d = deps();
		const got = await pairFromCode(`tallpine-mobile://pair#${code}`, d);

		expect(got.ok).toBe(true);
		expect(d.saveHost).toHaveBeenCalledWith(
			expect.objectContaining({
				id: "h_paired",
				name: "dev-mbp",
				endpoints: [lan, tunnel],
				token: "pw",
			}),
		);
	});

	it("returns a config for the endpoint that won the race", async () => {
		const got = await pairFromCode(`tallpine-mobile://pair#${code}`, deps());

		expect(got.ok).toBe(true);
		if (got.ok) expect(got.config.host).toBe("192.168.1.42");
	});

	it("makes the newly scanned computer active", async () => {
		const d = deps();
		await pairFromCode(`tallpine-mobile://pair#${code}`, d);

		expect(d.setActiveHost).toHaveBeenCalledWith("h_paired");
	});

	// Verifying before storing is the existing behaviour and worth keeping: a
	// pairing that is saved without being proven leaves the app looking
	// connected to something it has never reached.
	it("verifies the winning endpoint before storing anything", async () => {
		const order: string[] = [];
		const d = deps({
			verify: vi.fn(async () => void order.push("verify")),
			saveHost: vi.fn(async () => void order.push("save")),
		});

		await pairFromCode(`tallpine-mobile://pair#${code}`, d);

		expect(order).toEqual(["verify", "save"]);
	});

	it("stores nothing when verification fails", async () => {
		const d = deps({
			verify: vi.fn(async () => {
				throw new Error("401");
			}),
		});
		const got = await pairFromCode(`tallpine-mobile://pair#${code}`, d);

		expect(got.ok).toBe(false);
		if (!got.ok) expect(got.reason).toBe("verify-failed");
		expect(d.saveHost).not.toHaveBeenCalled();
	});

	// A rejected password used to read as "Reached nothing at <LAN>": the
	// screen only saw "verify-failed" and treated it as unreachable.
	it("reports what the winning endpoint answered when verification fails", async () => {
		const d = deps({
			verify: vi.fn(async () => {
				throw Object.assign(new Error("401 Unauthorized"), { status: 401 });
			}),
		});
		const got = await pairFromCode(`tallpine-mobile://pair#${code}`, d);

		expect(got).toMatchObject({ ok: false, reason: "verify-failed", status: 401, endpoint: lan });
	});

	it("reports an unusable code rather than throwing", async () => {
		const got = await pairFromCode("definitely not a pairing code", deps());

		expect(got.ok).toBe(false);
		if (!got.ok) expect(got.reason).toBe("not-tallpine-qr");
	});

	it("reports when none of the code's endpoints answer", async () => {
		const d = deps({ race: vi.fn(async () => ({ ok: false as const, reason: "none-reachable" as const })) });
		const got = await pairFromCode(`tallpine-mobile://pair#${code}`, d);

		expect(got.ok).toBe(false);
		if (!got.ok) expect(got.reason).toBe("none-reachable");
	});

	// v1 is no longer accepted. Those codes predate the identity probe the race
	// uses, so a daemon old enough to emit one answers 404 to every probe and
	// the race could never complete — the app now says to update the desktop
	// rather than failing obscurely. See isLegacyPairingCode.
	it("refuses a legacy v1 code", async () => {
		const d = deps();
		const got = await pairFromCode(
			JSON.stringify({ v: 1, host: "192.168.1.42", port: 3011, password: "old" }),
			d,
		);

		expect(got.ok).toBe(false);
		expect(d.saveHost).not.toHaveBeenCalled();
	});
});

// The reason must not depend on which caller reached pairFromCode: the scan
// screen checks the code itself before calling, but any other entry point (a
// deep link, a paste) would otherwise get "not an Tallpine code" for a code Tallpine made.
describe("pairFromCode reasons", () => {
	it("reports an outdated desktop rather than an unrecognised code", async () => {
		const got = await pairFromCode(JSON.stringify({ v: 1, host: "192.168.1.42", port: 3011 }), deps());
		expect(got.ok).toBe(false);
		if (!got.ok) expect(got.reason).toBe("outdated-desktop");
	});

	it("still reports genuinely unrecognised input as not an Tallpine code", async () => {
		const got = await pairFromCode("hello world", deps());
		expect(got.ok).toBe(false);
		if (!got.ok) expect(got.reason).toBe("not-tallpine-qr");
	});

	it("preserves endpoint information when race fails for error reporting", async () => {
		const d = deps({ race: vi.fn(async () => ({ ok: false as const, reason: "none-reachable" as const })) });
		const got = await pairFromCode(`tallpine-mobile://pair#${code}`, d);

		expect(got.ok).toBe(false);
		if (!got.ok) {
			expect(got.reason).toBe("none-reachable");
		}
	});

	it("makes parsed endpoints available for error reporting in the UI layer", () => {
		// Verify that parsePairingCode correctly extracts endpoints that can be
		// used in error messages when the race fails. This is the contract that
		// pair.tsx relies on when constructing error targets.
		const parsed = parsePairingCode(`tallpine-mobile://pair#${code}`);
		expect(parsed).not.toBeNull();
		expect(parsed?.endpoints).toHaveLength(2);
		expect(parsed?.endpoints[0]).toEqual(lan);
		expect(parsed?.endpoints[1]).toEqual(tunnel);
		// The UI extracts the first endpoint for error reporting: host:port
		expect(parsed?.endpoints[0].host).toBe("192.168.1.42");
		expect(parsed?.endpoints[0].port).toBe(3011);
	});

	describe("pins from an untrusted offer", () => {
		const SAVED = "ab".repeat(32);
		const INJECTED = "cd".repeat(32);
		const offerCode = encodePairingCode({ v: 2, hostId: "h_paired", name: "m", platform: "darwin", endpoints: [{ ...lan, pin: INJECTED }], token: "pw" });

		it("does not register anything just by parsing the code", () => {
			resetPinRegistrationForTest();
			setPins.mockClear();
			parsePairingCode(offerCode);
			expect(setPins).not.toHaveBeenCalled();
		});

		it("restores the saved computer's pin when the pairing fails", async () => {
			resetPinRegistrationForTest();
			registerPins([{ ...lan, secure: true, pin: SAVED }]);
			setPins.mockClear();
			const d = deps({ verify: vi.fn(async () => { throw new Error("401"); }) });
			const got = await pairFromCode(offerCode, d);
			expect(got.ok).toBe(false);
			expect(setPins).toHaveBeenNthCalledWith(1, [`192.168.1.42:3011=${INJECTED}`]);
			expect(setPins).toHaveBeenLastCalledWith([`192.168.1.42:3011=${SAVED}`]);
		});

		it("keeps the offered pin once the pairing succeeds", async () => {
			resetPinRegistrationForTest();
			registerPins([{ ...lan, secure: true, pin: SAVED }]);
			setPins.mockClear();
			const got = await pairFromCode(offerCode, deps());
			expect(got.ok).toBe(true);
			expect(setPins).not.toHaveBeenCalledWith([`192.168.1.42:3011=${SAVED}`]);
		});
	});
});

describe("pairFromCode progress steps", () => {
	it("reports each step in order and only after the work it names succeeded", async () => {
		const steps: string[] = [];
		const d = deps({
			exchange: vi.fn(async () => ({ ok: true as const, token: "device-token" })),
			onStep: (id: string, state: string) => steps.push(`${id}:${state}`),
		});
		const code2 = encodePairingCode({ v: 2, hostId: "h_paired", name: "dev-mbp", platform: "darwin", endpoints: [lan], token: "pw-steps" });

		const got = await pairFromCode(`tallpine-mobile://pair#${code2}`, d);

		expect(got.ok).toBe(true);
		expect(steps).toEqual(["find:active", "find:done", "security:active", "security:done", "paired:active", "paired:done"]);
	});

	it("marks the security step failed when the code was already used", async () => {
		const steps: string[] = [];
		const notes: (string | undefined)[] = [];
		const d = deps({
			exchange: vi.fn(async () => ({ ok: false as const, status: 401, code: "PAIRING_CODE_USED" })),
			onStep: (id: string, state: string, note?: string) => { steps.push(`${id}:${state}`); notes.push(note); },
		});
		const code3 = encodePairingCode({ v: 2, hostId: "h_paired", name: "dev-mbp", platform: "darwin", endpoints: [lan], token: "pw-used" });

		await pairFromCode(`tallpine-mobile://pair#${code3}`, d);

		expect(steps.at(-1)).toBe("security:failed");
		expect(notes.at(-1)).toBe("the code was already used");
		expect(steps).not.toContain("paired:active");
	});
});
