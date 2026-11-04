import { FileText } from "lucide-react";
import { Globe } from "lucide-react";
import { useOpenArtifactPreview } from "../../hooks/useOpenArtifactPreview";
import type { SessionArtifact, WorkspaceSession } from "../../types/workspace";

/**
 * One row in the Summary panel's Artifacts list. HTML artifacts open in the
 * Browser panel; markdown/file artifacts open in the inspector's Artifacts view,
 * since artifact files live outside the git workspace.
 */
export function ArtifactSummaryCard({
	artifact,
	onOpenArtifact,
	session,
}: {
	artifact: SessionArtifact;
	onOpenArtifact?: (target: { feedback?: boolean; path: string }) => void;
	session: WorkspaceSession;
}) {
	const openArtifactPreview = useOpenArtifactPreview(session.id, session.hostId);
	const handleOpen = () => {
		if (artifact.kind === "html" && artifact.previewUrl) {
			openArtifactPreview(artifact.previewUrl);
			return;
		}
		onOpenArtifact?.({ path: artifact.path });
	};
	return (
		<div className="flex w-full min-w-0 items-center rounded-md border border-(--color-border-settings-input) text-sm transition-colors hover:bg-interactive-hover focus-within:bg-interactive-hover focus-within:ring-1 focus-within:ring-ring">
			<button
				className="flex min-w-0 flex-1 items-center gap-2 px-2.5 py-1.5 text-left outline-none"
				onClick={handleOpen}
				type="button"
			>
				{artifact.kind === "html" ? (
					<Globe aria-hidden="true" className="size-icon-sm shrink-0 text-settings-muted" />
				) : (
					<FileText aria-hidden="true" className="size-icon-sm shrink-0 text-settings-muted" />
				)}
				<span className="min-w-0 flex-1 truncate">{artifact.name}</span>
			</button>
		</div>
	);
}
