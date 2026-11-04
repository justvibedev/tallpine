import { AppLink } from "../AppLink";
import type { TFunction } from "i18next";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
	type InspectorInlineComment,
	type InspectorReviewLabels,
	type InspectorReviewSummaryAction,
} from "@tallpine/product-ui";
import type { components } from "../../../api/schema";
import type { WorkspaceSession } from "../../types/workspace";
import { ProjectConfig } from "./types";

export function canRequestPRRereview(verdict: string | undefined, pullRequestUrl: string | undefined): boolean {
	if (!pullRequestUrl) return false;
	return verdict !== "approved";
}

export function reviewLabels(t: TFunction): InspectorReviewLabels {
	return {
		tallpineSource: t("inspector.reviewBySource.ao"),
		bot: t("inspector.bot"),
		earlierPass: t("inspector.earlierPass"),
		githubSource: t("inspector.reviewBySource.github"),
		loadingReviews: t("inspector.loadingReviews"),
		loadMoreReviews: (count) => t("inspector.loadMoreReviews", { count }),
		noPastReviewSummaries: t("inspector.noPastReviewSummaries"),
		notInjected: t("inspector.review.notInjected"),
		openComments: t("inspector.openComments"),
		openInTallpineBrowser: t("inspector.openInTallpineBrowser"),
		openInSystemBrowser: t("inspector.openInSystemBrowser"),
		openInlineComments: (count) => t("inspector.openInlineComments", { count }),
		requestRereviewPR: t("inspector.requestRereviewPR"),
		reviewActions: t("inspector.reviewActions"),
		reviews: t("inspector.reviews"),
		reviewedAt: (time) => t("inspector.reviewedAt", { time }),
		resolvedComments: (count) => t("inspector.resolvedComments", { count }),
		rereviewRequested: t("inspector.rereviewRequested"),
		rereviewRequestFailed: t("inspector.rereviewRequestFailed"),
		resolveComment: t("inspector.resolveComment"),
		resolvedReview: t("inspector.resolvedReview"),
		resolveReviewFailed: t("inspector.resolveReviewFailed"),
		sendToWorkerAgent: t("inspector.sendToWorkerAgent"),
		sentToWorkerAgent: t("inspector.sentToWorkerAgent"),
		sendToWorkerAgentError: t("inspector.sendToWorkerAgentError"),
		workerAgentWorkingOnFeedback: t("inspector.workerAgentWorkingOnFeedback"),
		showLatestReviewOnly: t("inspector.showLatestReviewOnly"),
		showLess: t("inspector.showLess"),
		showMore: t("inspector.showMore"),
		commentNumber: (number) => t("inspector.commentNumber", { number }),
		unresolvedCount: (count) => t("inspector.unresolvedCount", { count }),
		viewInFile: t("inspector.viewInFile"),
		viewInFileWorkInProgress: t("inspector.viewInFileWorkInProgress"),
		viewOnPR: t("inspector.viewOnPR"),
	};
}

export function renderReviewMarkdown(body: string) {
	return (
		<ReactMarkdown
			components={{
				a: ({ href, children }) => (
					<AppLink href={href} target="_blank" rel="noopener noreferrer">
						{children}
					</AppLink>
				),
			}}
			remarkPlugins={[remarkGfm]}
		>
			{body}
		</ReactMarkdown>
	);
}

export function buildReviewerAgentConfig(
	existing: WorkspaceSession["reviewerConfig"] | undefined,
	model: string,
	mode: string,
): { model?: string; mode?: string; permissions?: string } | undefined {
	const next = { ...existing };
	if (model) next.model = model;
	else delete next.model;
	if (mode) next.mode = mode;
	else delete next.mode;
	return Object.keys(next).length > 0 ? next : undefined;
}

export function formatInlineReviewCommentMessage(comment: InspectorInlineComment & { reviewerId?: string }): string {
	const reviewer = sanitizeWorkerMessagePart(comment.reviewerId?.trim() || "unknown reviewer");
	const file = sanitizeWorkerMessagePart(comment.file?.trim() || "");
	const location = file ? `${file}${comment.line ? `:${comment.line}` : ""}` : "general PR comment";
	const body = sanitizeWorkerMessagePart(comment.body?.trim() || "No comment body provided.");
	const url = sanitizeWorkerMessagePart(comment.url?.trim() || "");
	const lines = [
		`A reviewer left an unresolved inline comment on your PR. Address it, commit the fix, and push the branch to GitHub.`,
		"",
		`Reviewer: @${reviewer}`,
		`Location: ${location}`,
		"",
		"Comment:",
		body,
	];
	if (url) {
		lines.push("", `Comment URL: ${url}`);
	}
	lines.push("", "You should not need to re-fetch review data unless you need additional context beyond what Tallpine has provided here.");
	return lines.join("\n");
}

export function formatReviewSummaryMessage(summary: InspectorReviewSummaryAction): string {
	const reviewer = sanitizeWorkerMessagePart(summary.reviewerId.trim() || "reviewer");
	const body = sanitizeWorkerMessagePart(summary.body.trim());
	const url = sanitizeWorkerMessagePart((summary.url || summary.pullRequestUrl || "").trim());
	const source = summary.source === "agent" ? "Tallpine agent review" : "external PR review";
	const lines = [
		`A ${source} from ${reviewer} has feedback for your pull request. Address the actionable items, run relevant tests, commit the fixes, and push the branch.`,
		"",
		"Review summary:",
		body,
	];
	if (url) lines.push("", `Review URL: ${url}`);
	return lines.join("\n");
}

export function sanitizeWorkerMessagePart(value: string): string {
	return value.replace(/[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F]/g, "");
}

export function projectConfig(project: components["schemas"]["ProjectOrDegraded"] | undefined): ProjectConfig | undefined {
	if (!project || !("config" in project)) return undefined;
	return project.config;
}

export function mockProjectConfig(): ProjectConfig {
	return {
		worker: { agent: "codex" },
		orchestrator: { agent: "codex" },
		reviewers: [{ harness: "codex" }],
	};
}
