/** Review diff body with empty and loading states. */
import { type ReactNode } from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { type WorkspaceCompareMode, type WorkspaceFileDetail } from "../../hooks/workspace-files/workspace-files-types";
import { useParsedDiff } from "../../hooks/useParsedDiff";
import { ImageDiffView } from "../ImageDiffView";
import { FileAnnotationModel } from "./diff-annotation-types";
import { PanelMessage } from "./DiffChrome";
import { DiffView } from "./DiffView";

export function emptyDiffMessage(compareMode: WorkspaceCompareMode | undefined, t: TFunction): string {
	return compareMode === "base" ? t("files.noChangesBase") : t("files.noChangesHead");
}


export function ReviewDiffBody({
	annotation,
	detail,
	detailLoadedAt,
	emptyFallback,
	filePath,
	onActiveSelectionChange,
	sessionId,
	hostId,
	split,
	wrap,
}: {
	annotation: FileAnnotationModel;
	detail: WorkspaceFileDetail;
	detailLoadedAt: number;
	emptyFallback?: ReactNode;
	filePath: string;
	onActiveSelectionChange: (active: boolean) => void;
	sessionId: string;
	hostId?: string;
	split: boolean;
	wrap: boolean;
}) {
	const { t } = useTranslation();
	void filePath;
	const { rows, pending } = useParsedDiff(detail.diff);
	// An image has no readable line diff, so it renders as the images themselves
	// rather than the binary placeholder.
	if (detail.imageMediaType) {
		return (
			<ImageDiffView
				path={detail.path}
				sessionId={sessionId}
				hostId={hostId}
				split={split}
				status={detail.status}
				version={detailLoadedAt}
			/>
		);
	}
	if (detail.binary) {
		return <PanelMessage compact>{t("files.binaryUnavailable")}</PanelMessage>;
	}
	if (pending) {
		return <PanelMessage compact>{t("files.loadingDiff")}</PanelMessage>;
	}
	if (rows.length === 0) {
		return emptyFallback ?? <PanelMessage compact>{emptyDiffMessage(detail.compareMode, t)}</PanelMessage>;
	}
	return (
		<DiffView
			annotation={annotation}
			onActiveSelectionChange={onActiveSelectionChange}
			path={detail.path}
			previousPath={detail.previousPath}
			rows={rows}
			split={split}
			truncated={detail.diffTruncated}
			wrap={wrap}
		/>
	);
}
