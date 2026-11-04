import { render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { buildPairingOffer, pairingCodeUrl } from "../../lib/pairing-payload";

const paintQr = vi.fn();
vi.mock("./qr-paint", async (importOriginal) => ({
	...(await importOriginal<typeof import("./qr-paint")>()),
	paintQr: (...args: unknown[]) => paintQr(...args),
}));

import { QrCanvas } from "./QrCanvas";

// The offer the desktop panel encoded for a LAN-only machine (token redacted
// to the same 8-character length).
const LAN_CODE = pairingCodeUrl(
	buildPairingOffer({
		hostId: "h_528372d8-5af2-4341-9c44-4fb9929ee599",
		name: "192.168.1.196",
		platform: "",
		password: "XXXXXXXX",
		endpoints: [{ kind: "lan", host: "192.168.1.196", port: 65221, secure: false }],
	}),
	"tallpine-mobile://pair",
);

beforeEach(() => {
	vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({
		setTransform: vi.fn(),
		clearRect: vi.fn(),
	} as unknown as CanvasRenderingContext2D);
});

afterEach(() => {
	vi.restoreAllMocks();
	paintQr.mockReset();
});

describe("QrCanvas", () => {
	// A pinned version 17 with a logo cut-out was too dense for an iPhone to
	// read off the 240px settings panel.
	it("draws a pairing code at its smallest version, with no logo", () => {
		render(<QrCanvas value={LAN_CODE} size={240} />);
		expect(paintQr).toHaveBeenCalledTimes(1);
		const [, matrix, options] = paintQr.mock.calls[0] as [unknown, { version: number }, { showLogo: boolean; logo: unknown }];
		expect(matrix.version).toBeLessThanOrEqual(12);
		expect(options.showLogo).toBe(false);
		expect(options.logo).toBeNull();
	});
});
