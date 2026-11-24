/**
 * The URL "Open in default browser" should open while a passkey notice shows.
 *
 * Mid-flow identity-provider pages (accounts.google.com "Verifying it's you")
 * only work with the in-app profile's sign-in state; opened in the system
 * browser they show an error. So the notice opens the page the user was on
 * before the identity-provider redirect, or the provider's origin root when
 * there is none, and never a mid-flow URL carrying flow tokens.
 */
const IDP_HOST_LABELS = new Set(["accounts", "account", "login", "signin", "auth", "sso", "id", "idp", "oauth", "identity", "passport"]);

function parse(url: string): URL | null {
	try {
		const parsed = new URL(url);
		return parsed.protocol === "http:" || parsed.protocol === "https:" ? parsed : null;
	} catch {
		return null;
	}
}

/** True for a http(s) URL on a host that looks like an identity provider (accounts.*, login.*, auth.*, …). */
export function isIdentityProviderUrl(url: string): boolean {
	const parsed = parse(url);
	if (!parsed) return false;
	const labels = parsed.hostname.split(".");
	return labels.length > 2 && IDP_HOST_LABELS.has(labels[0]?.toLowerCase() ?? "");
}

/** The tab's last http(s) top-level URL that is not an identity-provider page, else the previous one. */
export function trackLastNonIdpUrl(previous: string | undefined, url: string): string | undefined {
	return parse(url) && !isIdentityProviderUrl(url) ? url : previous;
}

/** URL that works standalone in another browser for a passkey page at `current`. */
export function passkeyExternalUrl(current: string, lastNonIdpUrl: string | undefined): string | undefined {
	if (!parse(current)) return undefined;
	if (!isIdentityProviderUrl(current)) return current;
	if (lastNonIdpUrl && parse(lastNonIdpUrl)) return lastNonIdpUrl;
	return `${new URL(current).origin}/`;
}
