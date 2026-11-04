import type { SessionArtifact } from "./api";

/** Newest first, so what the agent just wrote is on top. */
export function sortArtifacts(files: SessionArtifact[]): SessionArtifact[] {
	return [...files].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt) || a.path.localeCompare(b.path));
}

import { formatBytes } from "@tallpine/shared/format/bytes";

export const artifactSizeLabel = (bytes: number): string => formatBytes(bytes, { wholeKb: true });

export function artifactIcon(kind: SessionArtifact["kind"]): "globe" | "file-text" | "file" {
	return kind === "html" ? "globe" : kind === "markdown" ? "file-text" : "file";
}
