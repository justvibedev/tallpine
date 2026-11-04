import type { SessionStatus } from "./session-models";

export type PRPipelineFacts = {
	ci?: "failing" | "passing" | "pending" | string;
	draft?: boolean;
	/** Aggregate review verdict: "approved" | "changes_requested" | "pending"(review required) | none. */
	review?: string;
	reviewComments?: boolean;
	/** true = mergeable, false = blocked, undefined = unknown. */
	mergeable?: boolean;
};

/** Mirror of backend/pkg/contract/status.go prPipelineStatus: the one PR status order. */
export function prPipelineStatus(pr: PRPipelineFacts): SessionStatus {
	if (pr.ci === "failing") return "ci_failed";
	if (pr.draft) return "draft";
	if (pr.review === "changes_requested") return "changes_requested";
	if (pr.reviewComments) return "commented";
	if (pr.mergeable === true) return "mergeable";
	if (pr.review === "pending") return "review_pending";
	if (pr.mergeable === false) return "pr_open";
	if (pr.review === "approved") return "approved";
	return "pr_open";
}
