import { describe, expect, it } from "vitest";
import { NORMAL_LENS, pickNormalLens } from "./cameraLens";

const lens = (deviceType: string, localizedName: string) => ({ deviceType, localizedName });
const WIDE = lens(NORMAL_LENS, "Back Camera");
const ULTRA = lens("AVCaptureDeviceTypeBuiltInUltraWideCamera", "Back Ultra Wide Camera");
const TELE = lens("AVCaptureDeviceTypeBuiltInTelephotoCamera", "Back Telephoto Camera");
const DUAL_WIDE = lens("AVCaptureDeviceTypeBuiltInDualWideCamera", "Back Dual Wide Camera");
const TRIPLE = lens("AVCaptureDeviceTypeBuiltInTripleCamera", "Back Triple Camera");

describe("pickNormalLens", () => {
	it("picks the plain 1x lens over the ultra-wide and the virtual devices", () => {
		expect(pickNormalLens([WIDE, DUAL_WIDE, TELE, TRIPLE, ULTRA])).toBe(NORMAL_LENS);
	});

	it("picks it on a single-camera phone", () => {
		expect(pickNormalLens([WIDE])).toBe(NORMAL_LENS);
	});

	// Matching is by device type, so a localized display name doesn't matter.
	it("ignores the localized name", () => {
		expect(pickNormalLens([lens(NORMAL_LENS, "Rückkamera"), ULTRA])).toBe(NORMAL_LENS);
	});

	// A selectedLens matching no device leaves the session with no camera, so an
	// empty or unrecognisable list must defer to the native default.
	it("returns undefined for an empty list", () => {
		expect(pickNormalLens([])).toBeUndefined();
	});

	it("returns undefined when every lens is a specialised optic", () => {
		expect(pickNormalLens([ULTRA, TRIPLE])).toBeUndefined();
	});
});
