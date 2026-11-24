import { describe, expect, it } from "vitest";
import {
	isAutoAllowedSitePermission,
	isConfirmableExternalUrl,
	sitePermissionKinds,
	sitePermissionOrigin,
} from "./browser-site-permissions";

describe("site permission mapping", () => {
	it("maps Electron's media request and check details to camera and microphone", () => {
		expect(sitePermissionKinds("media", { mediaTypes: ["video", "audio"] })).toEqual(["camera", "microphone"]);
		expect(sitePermissionKinds("media", { mediaTypes: ["audio", "audio"] })).toEqual(["microphone"]);
		expect(sitePermissionKinds("media", { mediaType: "video" })).toEqual(["camera"]);
		expect(sitePermissionKinds("media", { mediaType: "unknown" })).toBeUndefined();
		expect(sitePermissionKinds("media", {})).toBeUndefined();
	});

	it("offers only the kinds the bar knows", () => {
		expect(sitePermissionKinds("notifications")).toEqual(["notifications"]);
		expect(sitePermissionKinds("clipboard-read")).toEqual(["clipboard-read"]);
		for (const permission of ["geolocation", "midi", "openExternal", "display-capture", "usb"]) {
			expect(sitePermissionKinds(permission)).toBeUndefined();
		}
	});

	it("auto-allows fullscreen and pointer lock only", () => {
		expect(isAutoAllowedSitePermission("fullscreen")).toBe(true);
		expect(isAutoAllowedSitePermission("pointerLock")).toBe(true);
		expect(isAutoAllowedSitePermission("notifications")).toBe(false);
	});

	it("keys decisions by http(s) origin", () => {
		expect(sitePermissionOrigin("https://meet.example/room?x=1")).toBe("https://meet.example");
		expect(sitePermissionOrigin("http://localhost:3000/")).toBe("http://localhost:3000");
		expect(sitePermissionOrigin("file:///tmp/a.html")).toBeUndefined();
		expect(sitePermissionOrigin("not a url")).toBeUndefined();
	});

	it("confirms only mailto: and tel: links", () => {
		expect(isConfirmableExternalUrl("mailto:a@example.com")).toBe(true);
		expect(isConfirmableExternalUrl("tel:+15551234")).toBe(true);
		expect(isConfirmableExternalUrl("zoommtg://join")).toBe(false);
		expect(isConfirmableExternalUrl("https://example.com")).toBe(false);
	});
});
