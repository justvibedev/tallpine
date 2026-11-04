import { ArtifactsViewMock } from "./SessionView.test.mocks.files";
import { type ReactNode } from "react";
import { vi } from "vitest";
import { type InspectorView } from "../stores/ui-store";


const inspectorVisibilityRenders = vi.hoisted(() => [] as boolean[]);

vi.mock("./SessionInspector", () => ({
	SessionInspector: ({
		artifactRequest,
		filesView,
		isInspectorVisible = true,
		onArtifactRequestConsumed,
		onOpenArtifact,
		onOpenFiles,
		onOpenReviewFile,
		onOpenReviewerChat,
		onToggleBrowserPopOut,
		onViewChange,
		onWorkerMessageSent,
		view,
	}: {
		artifactRequest?: { feedback?: boolean; path: string; key: number } | null;
		filesView?: ReactNode;
		isInspectorVisible?: boolean;
		onArtifactRequestConsumed?: (key: number) => void;
		onOpenArtifact?: (target: { feedback?: boolean; path: string }) => void;
		onOpenFiles?: () => void;
		onOpenReviewFile?: (target: { line?: number; path: string }) => void;
		onOpenReviewerChat?: (reviewId: string) => void;
		onToggleBrowserPopOut?: (next: boolean) => void;
		onViewChange?: (view: InspectorView) => void;
		onWorkerMessageSent?: () => void;
		view?: string;
	}) => {
		inspectorVisibilityRenders.push(isInspectorVisible);
		return (
			<div>
				<button role="tab" type="button" onClick={() => onViewChange?.("summary")}>
					Summary
				</button>
				<button role="tab" type="button" onClick={() => onViewChange?.("reviews")}>
					Reviews
				</button>
				<button role="tab" type="button" onClick={() => onViewChange?.("browser")}>
					Browser
				</button>
				<div data-browser-dock-target="">
					<button
						type="button"
						data-view={view}
						onClick={() => onToggleBrowserPopOut?.(true)}
					>
						pop browser
					</button>
				</div>
				<button type="button" onClick={onOpenFiles}>
					open files
				</button>
				<button type="button" onClick={() => onOpenArtifact?.({ feedback: true, path: "report.html" })}>
					open artifact feedback
				</button>
				<button type="button" onClick={() => onOpenReviewFile?.({ path: "src/panel.tsx", line: 42 })}>
					view review file
				</button>
				<button type="button" onClick={() => onOpenReviewFile?.({ path: "notes.txt" })}>
					view review basename
				</button>
				<button type="button" onClick={() => onOpenReviewerChat?.("review-1")}>
					open reviewer chat
				</button>
				<button type="button" onClick={onWorkerMessageSent}>
					send review feedback
				</button>
				{view === "files" ? filesView : null}
				{view === "artifacts" ? (
					<ArtifactsViewMock onRequestConsumed={onArtifactRequestConsumed} request={artifactRequest} />
				) : null}
			</div>
		);
	},
}));

export { inspectorVisibilityRenders };
