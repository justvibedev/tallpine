// Picks which rear lens the QR scanner should use on iOS. Pure, so it can be
// unit-tested without a camera — mirroring `pushStatus.ts`.
//
// Since expo-camera 58, getAvailableLensesAsync() returns LensInfo objects and
// `selectedLens` is matched natively against AVCaptureDevice.deviceType.rawValue
// (CameraSessionManager.updateDevice), so we pick by the stable device type
// instead of the localized display name.
//
// The default is the problem this solves: iOS hands back a *virtual* device
// ("Back Dual Wide Camera" / "Back Triple Camera") whose widest member is the
// 0.5x ultra-wide, so the preview opens zoomed out. Ultra-wide is the worst lens
// for scanning — the code covers fewer pixels, so it must be held much closer,
// and the ultra-wide's longer minimum focus distance makes close-ups soft.

/** deviceType of the plain 1x rear lens. */
export const NORMAL_LENS = "AVCaptureDeviceTypeBuiltInWideAngleCamera";

export type LensChoice = { deviceType: string; localizedName?: string };

/**
 * Chooses the normal (1x) lens from the lenses the camera reports, or undefined
 * to keep whatever the native default is.
 *
 * Returning undefined rather than guessing matters: `selectedLens` that matches
 * no device leaves the session without a camera, so a wrong answer is worse than
 * no answer.
 */
export function pickNormalLens(lenses: readonly LensChoice[]): string | undefined {
	return lenses.some((l) => l.deviceType === NORMAL_LENS) ? NORMAL_LENS : undefined;
}
