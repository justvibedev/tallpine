/** One kind of file a worker can leave in its session-owned artifact directory. */
export type ArtifactKind = "html" | "markdown" | "file";

/**
 * One file present in a session's artifact directory, mirroring the daemon's
 * SessionArtifact wire shape. `previewUrl` is only set for `html` artifacts,
 * which route through the existing Browser preview flow; `markdown`/`file`
 * artifacts have no preview URL and open in the Files inspector instead.
 * `rawUrl` is set for every kind: it is the artifact preview origin's raw
 * byte fetch, a distinct host from the workspace preview origin, so it can
 * never resolve to a workspace-relative file of the same path.
 */
export type SessionArtifact = {
	kind: ArtifactKind;
	name: string;
	path: string;
	previewUrl?: string;
	rawUrl?: string;
	/**
	 * The page on its own inline origin, for framing it in the chat thread: its
	 * files load same-origin there, and the daemon refuses that origin. html only.
	 */
	inlineUrl?: string;
	size: number;
	updatedAt: string;
};
