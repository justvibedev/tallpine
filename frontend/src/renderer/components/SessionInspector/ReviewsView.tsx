import { memo } from "react";
import type { WorkspaceSession } from "../../types/workspace";
import { OpenReviewerTerminal } from "./types";
import { ReviewsSection } from "./ReviewsSection";

export const ReviewsView = memo(function ReviewsView({
	session,
	hostId,
	onOpenReviewFile,
	onOpenReviewerTerminal,
	onOpenReviewerChat,
	onWorkerMessageSent,
}: {
	session: WorkspaceSession;
	hostId?: string;
	onOpenReviewFile?: (target: { line?: number; path: string }) => void;
	onOpenReviewerTerminal?: OpenReviewerTerminal;
	onOpenReviewerChat?: (reviewId: string) => void;
	onWorkerMessageSent?: () => void;
}) {
	return (
		<div role="tabpanel">
			<ReviewsSection hostId={hostId} onOpenReviewFile={onOpenReviewFile} onOpenReviewerTerminal={onOpenReviewerTerminal} onOpenReviewerChat={onOpenReviewerChat} onWorkerMessageSent={onWorkerMessageSent} session={session} />
		</div>
	);
});
