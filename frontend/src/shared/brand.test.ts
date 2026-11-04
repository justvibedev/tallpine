import { describe, expect, it } from "vitest";
import * as brand from "./brand";

describe("brand", () => {
	it("does not collide with the official Agent Orchestrator install", () => {
		expect(brand.CLI_NAME).not.toBe("ao");
		expect(brand.HOME_DIR_NAME).not.toBe(".ao");
		expect(brand.DEFAULT_DAEMON_PORT).not.toBe(3001);
		expect(brand.DEV_DAEMON_PORT).not.toBe(3002);
		expect(brand.TMUX_SOCKET_NAME).not.toBe("ao");
		expect(brand.URL_SCHEME).not.toBe("ao-app");
		expect(brand.BUNDLE_ID).not.toContain("agent-orchestrator");
		expect(brand.MOBILE_WAKE_LABEL).toBe("ee.justdev.tallpine.mobile-wake");
	});

	it("names the daemon binary per platform", () => {
		expect(brand.daemonBinaryName("win32")).toBe("tallpine.exe");
		expect(brand.daemonBinaryName("darwin")).toBe("tallpine");
	});

	it("pins the Tallpine identity", () => {
		expect(brand.PRODUCT_NAME).toBe("Tallpine");
		expect(brand.BUNDLE_ID).toBe("ee.justdev.tallpine");
		expect(brand.CLI_NAME).toBe("tallpine");
		expect(brand.HOME_DIR_NAME).toBe(".tallpine");
		expect(brand.ENV_PREFIX).toBe("TALLPINE_");
		expect(brand.URL_SCHEME).toBe("tallpine");
		expect(brand.IN_APP_LINK_SCHEME).toBe("tallpine");
		expect(brand.TMUX_SOCKET_NAME).toBe("tallpine");
		expect(brand.LAUNCHD_LABEL_PREFIX).toBe("ee.justdev.tallpine");
		expect(brand.DEFAULT_DAEMON_PORT).toBe(3101);
		expect(brand.MOBILE_BUNDLE_ID).toBe("ee.justdev.tallpine.mobile");
		expect(brand.MOBILE_URL_SCHEME).toBe("tallpine-mobile");
	});
});
