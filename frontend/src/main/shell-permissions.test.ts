import { describe, expect, it } from "vitest";
import { installShellPermissionPolicy, isShellPermissionAllowed } from "./shell-permissions";

function install() {
	let check: (...a: any[]) => boolean = () => true;
	let request: (...a: any[]) => void = () => undefined;
	installShellPermissionPolicy({
		setPermissionCheckHandler: ((h: any) => { check = h; }) as any,
		setPermissionRequestHandler: ((h: any) => { request = h; }) as any,
	});
	return { check, request };
}

describe("shell permission policy", () => {
	it("allows only clipboard write and fullscreen, and only for the main frame", () => {
		expect(isShellPermissionAllowed("clipboard-sanitized-write", true)).toBe(true);
		expect(isShellPermissionAllowed("fullscreen", true)).toBe(true);
		expect(isShellPermissionAllowed("clipboard-sanitized-write", false)).toBe(false);
		expect(isShellPermissionAllowed("fullscreen", undefined)).toBe(false);
	});

	it.each(["media", "geolocation", "notifications", "clipboard-read", "midi", "usb", "display-capture"])(
		"denies %s in request and check handlers",
		(permission) => {
			const { check, request } = install();
			expect(check(null, permission, "x", { isMainFrame: true })).toBe(false);
			let granted: boolean | undefined;
			request(null, permission, (g: boolean) => { granted = g; }, { isMainFrame: true });
			expect(granted).toBe(false);
		},
	);

	it("denies a render iframe asking for clipboard write", () => {
		const { check, request } = install();
		expect(check(null, "clipboard-sanitized-write", "x", { isMainFrame: false })).toBe(false);
		let granted: boolean | undefined;
		request(null, "fullscreen", (g: boolean) => { granted = g; }, { isMainFrame: false });
		expect(granted).toBe(false);
	});
});
