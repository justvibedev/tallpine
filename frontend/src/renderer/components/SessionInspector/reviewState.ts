import type { TFunction } from "i18next";
import { formatTimeCompact } from "../../lib/format-time";
import { appI18n } from "../../i18n";
import { type PRReviewState, type ReviewRunFacts } from "../../lib/session-reviews";

export function githubVerdict(verdict: string, t: TFunction): { label: string; tone: "neutral" | "running" | "success" | "danger" } {
	switch (verdict) {
		case "approved":
			return { label: t("inspector.review.approved"), tone: "success" };
		case "changes_requested":
			return { label: t("inspector.review.changesRequested"), tone: "danger" };
		case "review_required":
			return { label: t("inspector.review.notRun"), tone: "neutral" };
		default:
			return { label: t("inspector.review.commented"), tone: "neutral" };
	}
}

/* The reviews view is assembled from two queries that live in different
   components, so the derivations both need are module-level rather than
   recomputed (and allowed to drift) in each. */

/** Every recorded reviewer pass per PR, so each reviewer keeps its own tab.
 *  Falls back to the state's own runs against a daemon predating the runs field. */
export function runsByPRFrom(openReviewStates: PRReviewState[], runs: ReviewRunFacts[]): Map<string, ReviewRunFacts[]> {
	const byPR = new Map<string, ReviewRunFacts[]>();
	for (const run of runs.filter(
		(run) => (run.status === "complete" || run.status === "delivered") && Boolean(run.body?.trim()),
	)) {
		byPR.set(run.prUrl, [...(byPR.get(run.prUrl) ?? []), run]);
	}
	if (runs.length === 0) {
		for (const state of openReviewStates) {
			const fallback = [state.latestRun, state.previousRun].filter(
				(run): run is ReviewRunFacts =>
					Boolean(run) &&
					(run!.status === "complete" || run!.status === "delivered") &&
					Boolean(run!.body?.trim()),
			);
			if (fallback.length > 0) byPR.set(state.prUrl, fallback);
		}
	}
	return byPR;
}

export function reviewRunHasOutcome(run: ReviewRunFacts | undefined): boolean {
	return Boolean(run?.verdict?.trim());
}

/** The PRs Tallpine has an agent review outcome for. */
export function triggeredReviewStatesFrom(openReviewStates: PRReviewState[], runs: ReviewRunFacts[]): PRReviewState[] {
	return openReviewStates.filter(
		(reviewState) =>
			reviewRunHasOutcome(reviewState.latestRun) ||
			reviewRunHasOutcome(reviewState.previousRun) ||
			runs.some((run) => run.prUrl === reviewState.prUrl && reviewRunHasOutcome(run)) ||
			reviewState.status === "up_to_date" ||
			reviewState.status === "changes_requested",
	);
}

export function tallpineReviewMeta(reviewState: PRReviewState): string {
	if (
		reviewState.status === "needs_review" &&
		(!reviewState.latestRun ||
			reviewState.latestRun.status === "failed" ||
			reviewState.latestRun.status === "cancelled")
	) {
		return appI18n.t("inspector.latestCommitNotReviewedMeta", { number: reviewState.prNumber });
	}
	const displayRun = reviewState.latestRun ?? reviewState.previousRun;
	if (displayRun?.createdAt) {
		return `#${reviewState.prNumber} · ${formatTimeCompact(displayRun.createdAt)}`;
	}
	if (!displayRun && (reviewState.status === "needs_review" || reviewState.status === "ineligible")) {
		return appI18n.t("inspector.notRunMeta", { number: reviewState.prNumber });
	}
	return `#${reviewState.prNumber}`;
}

// GitHub anchors a posted review at #pullrequestreview-<id> on the PR page; we
// only have that link once the run has been delivered to GitHub.
export function tallpineReviewCommentUrl(run: PRReviewState["latestRun"]): string | null {
	if (!run?.prUrl || !run.githubReviewId) return null;
	return `${run.prUrl}#pullrequestreview-${run.githubReviewId}`;
}

type ReviewVerdictLabel = { label: string; tone: "neutral" | "running" | "success" | "danger" };

export function reviewVerdict(reviewState: PRReviewState): ReviewVerdictLabel {
	const verdict = headVerdict(reviewState);
	// A verdict that never reached the PR must not read like a posted review.
	if (reviewState.latestRun?.postFailed && (reviewState.status === "up_to_date" || reviewState.status === "changes_requested")) {
		return { label: appI18n.t("inspector.review.notPosted", { verdict: verdict.label }), tone: "danger" };
	}
	return verdict;
}

function headVerdict(reviewState: PRReviewState): ReviewVerdictLabel {
	if (reviewState.status === "needs_review") {
		return { label: appI18n.t("inspector.review.needed"), tone: "neutral" };
	}
	if (reviewState.latestRun?.status === "failed") {
		return { label: appI18n.t("inspector.review.failed"), tone: "danger" };
	}
	if (reviewState.latestRun?.status === "cancelled") {
		return { label: appI18n.t("inspector.review.cancelled"), tone: "neutral" };
	}
	switch (reviewState.status) {
		case "running":
			return { label: appI18n.t("inspector.review.reviewing"), tone: "running" };
		case "up_to_date":
			return { label: appI18n.t("inspector.review.approved"), tone: "success" };
		case "changes_requested":
			return { label: appI18n.t("inspector.review.changesRequested"), tone: "danger" };
		case "ineligible":
			return { label: appI18n.t("inspector.review.notRun"), tone: "neutral" };
	}
	return { label: appI18n.t("inspector.review.notRun"), tone: "neutral" };
}
