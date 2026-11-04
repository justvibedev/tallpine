import { hasPin } from "./pinning";

// Expo replaces the global `fetch` with expo/fetch, which runs on its own native
// session. On iOS that session never consults the pinning handler, so a pinned
// (self-signed) gateway fails CA validation for every request. React Native's own
// fetch goes through RCTNetworking, where the pinned handler lives, so requests to
// a pinned host:port are sent that way. Android's expo/fetch already uses the
// pinned OkHttp client and is left alone.

type FetchFn = typeof fetch;

export type PinnedFetchDeps = {
	os: string;
	rnFetch: () => FetchFn;
	target: { fetch: FetchFn };
};

function targetOf(input: Parameters<FetchFn>[0]): { host: string; port: number } | null {
	try {
		const raw = typeof input === "string" ? input : input instanceof URL ? input.href : (input as Request).url;
		const url = new URL(raw);
		if (url.protocol !== "https:") return null;
		return { host: url.hostname, port: Number(url.port || 443) };
	} catch {
		return null;
	}
}

export function installPinnedFetch(deps: PinnedFetchDeps): void {
	if (deps.os !== "ios") return;
	const original = deps.target.fetch;
	let rn: FetchFn | undefined;
	deps.target.fetch = ((input, init) => {
		const t = targetOf(input);
		if (t && hasPin(t.host, t.port)) return (rn ??= deps.rnFetch())(input, init);
		return original(input, init);
	}) as FetchFn;
}
