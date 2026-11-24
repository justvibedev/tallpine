import { describe, expect, it } from "vitest";
import { isIdentityProviderUrl, passkeyExternalUrl, trackLastNonIdpUrl } from "./browser-passkey-url";

describe("browser passkey external URL", () => {
	it("recognises identity-provider hosts", () => {
		expect(isIdentityProviderUrl("https://accounts.google.com/v3/signin/challenge/pk?TL=abc")).toBe(true);
		expect(isIdentityProviderUrl("https://login.microsoftonline.com/x")).toBe(true);
		expect(isIdentityProviderUrl("https://login.example.com/x")).toBe(true);
		expect(isIdentityProviderUrl("https://example.com/accounts")).toBe(false);
		expect(isIdentityProviderUrl("not a url")).toBe(false);
	});

	it("tracks only non-IdP http(s) top-level URLs", () => {
		let last = trackLastNonIdpUrl(undefined, "https://app.example.com/dashboard");
		last = trackLastNonIdpUrl(last, "https://accounts.google.com/o/oauth2/auth?state=t");
		last = trackLastNonIdpUrl(last, "about:blank");
		expect(last).toBe("https://app.example.com/dashboard");
	});

	it("opens the pre-redirect page, never the mid-flow URL", () => {
		const mid = "https://accounts.google.com/v3/signin/challenge/pk?TL=secret";
		expect(passkeyExternalUrl(mid, "https://app.example.com/dashboard")).toBe("https://app.example.com/dashboard");
	});

	it("falls back to the IdP origin root without a prior page", () => {
		expect(passkeyExternalUrl("https://accounts.google.com/v3/signin/challenge/pk?TL=secret", undefined)).toBe("https://accounts.google.com/");
	});

	it("keeps an ordinary page's own URL and rejects non-web URLs", () => {
		expect(passkeyExternalUrl("https://example.com/signin?x=1", "https://other.com/")).toBe("https://example.com/signin?x=1");
		expect(passkeyExternalUrl("file:///x", undefined)).toBeUndefined();
	});
});
