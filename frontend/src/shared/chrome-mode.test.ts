import { describe, expect, it } from "vitest";
import { googleSignInStartURL, isGoogleSignInPage, isGoogleSignInRejected } from "./chrome-mode";

describe("Google sign-in pages (B854)", () => {
	it("recognizes every step of Google's sign-in, and nothing else", () => {
		for (const url of [
			"https://accounts.google.com/v3/signin/identifier?continue=x",
			"https://accounts.google.com/v3/signin/challenge/pwd",
			"https://accounts.google.com/v3/signin/rejected?continue=x",
			"https://accounts.google.com/ServiceLogin?service=analytics",
			"https://accounts.google.com/AccountChooser",
			"https://accounts.google.com/signin/v2/identifier",
			// A third-party "Sign in with Google" consent step is refused the same way.
			"https://accounts.google.com/signin/oauth/id?client_id=x",
		]) expect(isGoogleSignInPage(url), url).toBe(true);
		for (const url of ["https://accounts.google.com/", "https://myaccount.google.com/signin", "https://example.com/v3/signin/identifier", "not a url"])
			expect(isGoogleSignInPage(url), url).toBe(false);
		expect(isGoogleSignInRejected("https://accounts.google.com/v3/signin/identifier")).toBe(false);
	});

	it("plain Chrome opens where the sign-in continues, https only", () => {
		expect(googleSignInStartURL("https://accounts.google.com/v3/signin/rejected?continue=https%3A%2F%2Fanalytics.google.com%2Fanalytics%2Fweb%2F%23%2Fp1"))
			.toBe("https://analytics.google.com/analytics/web/#/p1");
		expect(googleSignInStartURL("https://accounts.google.com/v3/signin/identifier")).toBeUndefined();
		expect(googleSignInStartURL("https://accounts.google.com/v3/signin/identifier?continue=javascript%3Aalert(1)")).toBeUndefined();
		expect(googleSignInStartURL("https://example.com/?continue=https%3A%2F%2Fa.example%2F")).toBeUndefined();
	});
});
