import { describe, expect, it } from "vitest";
import { shouldOnboard, launchRedirect } from "./onboarding";

describe("shouldOnboard", () => {
	it("onboards a fresh install", () => {
		expect(shouldOnboard({ configured: false, skipped: false })).toBe(true);
	});

	it("does not onboard once a server is configured", () => {
		expect(shouldOnboard({ configured: true, skipped: false })).toBe(false);
	});

	it("does not onboard after the user skipped", () => {
		expect(shouldOnboard({ configured: false, skipped: true })).toBe(false);
	});

	it("does not onboard when configured and skipped", () => {
		expect(shouldOnboard({ configured: true, skipped: true })).toBe(false);
	});

	// Both flags load asynchronously. Acting on half-loaded state would bounce a
	// paired user onto the welcome screen for a frame on every cold start.
	it("waits while the config is still loading", () => {
		expect(shouldOnboard({ configured: null, skipped: false })).toBe(false);
	});

	it("waits while the skip flag is still loading", () => {
		expect(shouldOnboard({ configured: false, skipped: null })).toBe(false);
	});

	it("waits while both are still loading", () => {
		expect(shouldOnboard({ configured: null, skipped: null })).toBe(false);
	});
});

describe("launchRedirect", () => {
	it("lands a paired cold launch on Projects, not Workers", () => {
		expect(launchRedirect({ configured: true, skipped: false, pathname: "/" })).toBe("/projects");
		expect(launchRedirect({ configured: false, skipped: true, pathname: "/" })).toBe("/projects");
	});
	it("sends a first-run user to onboarding", () => {
		expect(launchRedirect({ configured: false, skipped: false, pathname: "/" })).toBe("/onboarding");
	});
	it("waits until both inputs are known", () => {
		expect(launchRedirect({ configured: null, skipped: false, pathname: "/" })).toBeNull();
		expect(launchRedirect({ configured: true, skipped: null, pathname: "/" })).toBeNull();
	});
	it("leaves a deep-linked launch alone", () => {
		expect(launchRedirect({ configured: true, skipped: false, pathname: "/session/abc" })).toBeNull();
	});
});
