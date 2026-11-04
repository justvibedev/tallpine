import { useCallback, useEffect, useState } from "react";
import { fetchRemoteImageDataUrl, isRemoteImageUrl } from "../lib/remote-image";

export type RemoteImageState = "local" | "idle" | "loading" | "loaded" | "failed";

/**
 * `src` to put on an `<img>` for `url`. Loopback and data URLs pass through.
 * A remote URL is fetched by the main process, on mount when `auto` (trusted
 * sources: SCM avatars, favicons) or on `load()` otherwise (agent output).
 */
export function useRemoteImage(url: string | undefined, auto: boolean): { state: RemoteImageState; src: string | undefined; load: () => void } {
	const remote = isRemoteImageUrl(url);
	const [loaded, setLoaded] = useState<{ url: string; src: string | null } | null>(null);
	const [requested, setRequested] = useState<string | null>(null);
	const wanted = remote && (auto || requested === url);

	useEffect(() => {
		if (!wanted || !url || loaded?.url === url) return;
		let cancelled = false;
		fetchRemoteImageDataUrl(url).then(
			(src) => { if (!cancelled) setLoaded({ url, src }); },
			() => { if (!cancelled) setLoaded({ url, src: null }); },
		);
		return () => { cancelled = true; };
	}, [wanted, url, loaded?.url]);

	const load = useCallback(() => setRequested(url ?? null), [url]);
	if (!remote) return { state: "local", src: url, load };
	const current = loaded?.url === url ? loaded : null;
	if (current) return current.src ? { state: "loaded", src: current.src, load } : { state: "failed", src: undefined, load };
	return { state: wanted ? "loading" : "idle", src: undefined, load };
}
