// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
	dockBounceType,
	osToastChimeControllable,
	shouldReplaceBounce,
	shouldSignalAttention,
	shouldToast,
	toastSilent,
} from "./notification-signals";


describe("shouldToast", () => {
	it("fires a toast for every backend notification type", () => {
		for (const type of ["needs_input", "ready_to_merge", "pr_merged", "pr_closed_unmerged", "plan_waiting"]) {
			expect(shouldToast({ title: `${type} title` }, true)).toBe(true);
		}
	});

	it("does not toast without a title or when notifications are unsupported", () => {
		expect(shouldToast({ title: "" }, true)).toBe(false);
		expect(shouldToast({}, true)).toBe(false);
		expect(shouldToast({ title: "needs input" }, false)).toBe(false);
	});
});

describe("shouldSignalAttention", () => {
	it("flashes for critical and attention urgency", () => {
		expect(shouldSignalAttention("critical")).toBe(true);
		expect(shouldSignalAttention("attention")).toBe(true);
	});

	it("does not flash for informational, unknown or missing urgency", () => {
		expect(shouldSignalAttention("info")).toBe(false);
		expect(shouldSignalAttention("some_future_urgency")).toBe(false);
		expect(shouldSignalAttention(undefined)).toBe(false);
	});
});

describe("shouldReplaceBounce", () => {
	it("replaces no bounce or a pending informational bounce", () => {
		expect(shouldReplaceBounce(null)).toBe(true);
		expect(shouldReplaceBounce({ critical: false })).toBe(true);
	});

	it("never replaces a pending critical bounce, so a blocked agent stays loud", () => {
		expect(shouldReplaceBounce({ critical: true })).toBe(false);
	});
});

describe("osToastChimeControllable", () => {
	// Platform contract: Electron's mac and win presenters read `silent`;
	// the Linux libnotify presenter does not (no suppress-sound hint).
	it.each(["darwin", "win32"] as const)("is controllable on %s", (platform) => {
		expect(osToastChimeControllable(platform)).toBe(true);
	});

	it.each(["linux", "freebsd", "openbsd"] as const)("is not controllable on %s", (platform) => {
		expect(osToastChimeControllable(platform)).toBe(false);
	});
});

describe("toastSilent", () => {
	const HONOURING: NodeJS.Platform[] = ["darwin", "win32"];

	it.each(HONOURING)("on %s mutes the toast whenever sound notifications are off", (platform) => {
		expect(toastSilent(platform, false, false)).toBe(true);
		expect(toastSilent(platform, false, true)).toBe(true);
	});

	it.each(HONOURING)("on %s mutes the toast when Tallpine plays its own sound", (platform) => {
		expect(toastSilent(platform, true, true)).toBe(true);
	});

	it.each(HONOURING)("on %s keeps the native chime for an informational toast when sound is on", (platform) => {
		expect(toastSilent(platform, true, false)).toBe(false);
	});

	// On Linux the value would be ignored, so no combination may claim a mute.
	it.each([
		[false, false],
		[false, true],
		[true, false],
		[true, true],
	])("on linux never claims control (enabled=%s, playsSound=%s)", (enabled, playsSound) => {
		expect(toastSilent("linux", enabled, playsSound)).toBeUndefined();
	});
});

describe("dockBounceType", () => {
	it("bounces critically only for critical urgency", () => {
		expect(dockBounceType("critical")).toBe("critical");
	});

	it("bounces once for attention, info, unknown or missing urgency", () => {
		for (const u of ["attention", "info", "some_future_urgency", undefined]) {
			expect(dockBounceType(u)).toBe("informational");
		}
	});
});
