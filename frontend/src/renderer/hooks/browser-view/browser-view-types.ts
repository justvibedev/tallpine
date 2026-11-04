/** Closed-tab and option types for useBrowserView. */

export type ClosedBrowserTab = {
	id: string;
	title: string;
	url: string;
	favicon?: string;
};


export type UseBrowserViewOptions = {
	sessionId: string;
	/** Source daemon for preview URLs; omitted for the local daemon. */
	origin?: { hostId: string; sessionId: string; proxyBase: string };
	active: boolean;
	poppedOut: boolean;
	/**
	 * When true, the view is cleared and the daemon-driven preview is suppressed.
	 * Use when the session is terminated: the old preview content should not
	 * remain visible even if the DB still carries a preview_url.
	 */
	terminated?: boolean;
	/**
	 * Preview target driven by the daemon (via `tallpine preview`, streamed over CDC).
	 * When set, the view navigates here automatically; an empty value clears it.
	 */
	previewUrl?: string;
	/**
	 * Monotonic counter the daemon bumps on every `tallpine preview` call, even when
	 * previewUrl is unchanged. The view re-navigates whenever it advances, so a
	 * repeated `tallpine preview <same-url>` still refreshes (and CDC replays of an
	 * unrelated session update, which leave it unchanged, are ignored).
	 */
	previewRevision?: number;
};
