import type { components } from "../../api/schema";

type ProjectConfig = components["schemas"]["ProjectConfig"];

/** What a project gets when it sets nothing; mirrors the daemon's domain.ReviewRisk* defaults. */
export const REVIEW_RISK_DEFAULTS = {
	paths: ["**/migrations/**"],
	harness: "claude-code",
	model: "claude-opus-5-5",
	effort: "high",
} as const;

/** The review fields of the project settings draft. */
export type ReviewDefaultsDraft = {
	/** null (or unset) inherits the app default. */
	autoReview?: boolean | null;
	terminateOnPrMerge?: boolean;
	/** Comma or line separated path globs; empty means the built-in list. */
	reviewRiskPaths?: string;
	reviewRiskFirstOnly?: boolean;
	/** Stronger reviewer; all three empty means the built-in Claude Code default. */
	reviewRiskHarness?: string;
	reviewRiskModel?: string;
	reviewRiskEffort?: string;
};

/** Splits the comma or line separated path field. */
export const splitReviewRiskPaths = (text: string | undefined): string[] => (text ?? "").split(/[,\n]/).map((p) => p.trim()).filter(Boolean);

/** The stronger reviewer a draft resolves to, with the built-in default filled in as the daemon does. */
export function effectiveStrongerReviewer(draft: ReviewDefaultsDraft): { harness: string; model: string; effort: string; builtIn: boolean } {
	const harness = draft.reviewRiskHarness ?? "";
	const model = (draft.reviewRiskModel ?? "").trim();
	const effort = draft.reviewRiskEffort ?? "";
	if (!harness && !model) {
		return { harness: REVIEW_RISK_DEFAULTS.harness, model: REVIEW_RISK_DEFAULTS.model, effort: effort || REVIEW_RISK_DEFAULTS.effort, builtIn: !effort };
	}
	const resolved = harness || REVIEW_RISK_DEFAULTS.harness;
	return { harness: resolved, model: model || (resolved === REVIEW_RISK_DEFAULTS.harness ? REVIEW_RISK_DEFAULTS.model : ""), effort, builtIn: false };
}

export function reviewDefaultsDraft(config: ProjectConfig): ReviewDefaultsDraft {
	const risk = config.reviewRisk;
	return {
		autoReview: config.autoReview ?? null,
		terminateOnPrMerge: config.terminateOnPrMerge ?? true,
		reviewRiskPaths: (risk?.paths ?? []).join(", "),
		reviewRiskFirstOnly: risk?.firstReviewOnly ?? false,
		reviewRiskHarness: risk?.harness ?? "",
		reviewRiskModel: risk?.model ?? "",
		reviewRiskEffort: risk?.effort ?? "",
	};
}

/**
 * The config fields a draft saves. Review settings belong to the project alone;
 * a flag still off and never stored stays out of the config. An empty reviewer, path list and
 * first-review-only flag leaves reviewRisk out so the built-in defaults apply.
 */
export function reviewDefaultsConfig(current: ProjectConfig, draft: ReviewDefaultsDraft): Pick<ProjectConfig, "autoReview" | "terminateOnPrMerge" | "reviewRisk"> {
	const model = draft.reviewRiskModel?.trim() ?? "";
	const harness = draft.reviewRiskHarness ?? "";
	const effort = draft.reviewRiskEffort ?? "";
	const paths = splitReviewRiskPaths(draft.reviewRiskPaths);
	const firstReviewOnly = draft.reviewRiskFirstOnly === true;
	const reviewRisk = harness || model || effort || paths.length || firstReviewOnly
		? {
			...(paths.length ? { paths } : {}),
			...(harness ? { harness } : {}),
			...(model ? { model } : {}),
			...(effort ? { effort } : {}),
			...(firstReviewOnly ? { firstReviewOnly } : {}),
		}
		: undefined;
	// terminateOnPrMerge is on unless set: an unstored true stays out of the config, an off must be stored.
	const onByDefault = (value: boolean | undefined, stored: boolean | null | undefined) => (value === false || stored != null ? value : undefined);
	// autoReview is tri-state: null inherits the app default and is left out of the config.
	void current.autoReview;
	return { autoReview: draft.autoReview ?? undefined, terminateOnPrMerge: onByDefault(draft.terminateOnPrMerge, current.terminateOnPrMerge), reviewRisk };
}
