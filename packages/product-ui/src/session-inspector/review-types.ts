/** Review data types for the inspector reviews view. */

export type InspectorVerdict = {
	label: string;
	tone: "neutral" | "running" | "success" | "danger";
};

export type InspectorReviewRun = {
	body?: string;
	createdAtLabel: string;
	harness: string;
	id: string;
	inlineComments?: InspectorInlineComment[];
	resolvedComments?: InspectorInlineComment[];
	status: string;
	url?: string | null;
	verdict: InspectorVerdict;
};

export type InspectorReviewSummaryAction = {
	body: string;
	pullRequestUrl?: string;
	reviewerId: string;
	source: "agent" | "external";
	url?: string | null;
};

export type InspectorInlineComment = {
	autoInjectReview?: boolean;
	body?: string;
	file?: string;
	line?: number;
	pullRequestUrl?: string;
	reviewerId?: string;
	resolved?: boolean;
	url?: string;
};

export type InspectorGithubReview = {
	body?: string;
	canRequestRereview?: boolean;
	id: string;
	inlineComments?: InspectorInlineComment[];
	isBot?: boolean;
	pullRequestUrl?: string;
	resolvedComments?: InspectorInlineComment[];
	reviewerId: string;
	reviewUrl?: string;
	submittedAt: string;
	submittedAtLabel: string;
	verdict: InspectorVerdict;
};

export type InspectorUnresolvedReviewer = {
	count: number;
	isBot?: boolean;
	links: {
		autoInjectReview?: boolean;
		body?: string;
		file?: string;
		line?: number;
		reviewId?: string;
		url?: string;
	}[];
	reviewerId: string;
	reviewUrl?: string;
};

export type InspectorReviewGroup = {
	tallpineState?: {
		dimmed?: boolean;
		historical?: boolean;
		notInjected?: boolean;
		runs: InspectorReviewRun[];
	};
	github?: {
		entries: InspectorGithubReview[];
		notInjected?: boolean;
		unresolved: number;
		unresolvedBy: InspectorUnresolvedReviewer[];
	};
	meta: string;
	number: number;
	/** Unique per PR; a multi-repository session can repeat a number across repositories. Defaults to the number. */
	key?: string;
	title: string;
	verdict?: InspectorVerdict;
};

export type InspectorReviewLabels = {
	tallpineSource: string;
	bot: string;
	earlierPass: string;
	githubSource: string;
	loadingReviews: string;
	loadMoreReviews: (count: number) => string;
	noPastReviewSummaries: string;
	notInjected: string;
	openComments: string;
	openInTallpineBrowser: string;
	openInSystemBrowser: string;
	openInlineComments: (count: number) => string;
	requestRereviewPR: string;
	reviewActions: string;
	reviews: string;
	reviewedAt: (time: string) => string;
	resolvedComments: (count: number) => string;
	rereviewRequested: string;
	rereviewRequestFailed: string;
	resolveComment: string;
	resolvedReview: string;
	resolveReviewFailed: string;
	sendToWorkerAgent: string;
	sentToWorkerAgent: string;
	sendToWorkerAgentError: string;
	workerAgentWorkingOnFeedback: string;
	showLatestReviewOnly: string;
	showLess: string;
	showMore: string;
	commentNumber: (number: number) => string;
	unresolvedCount: (count: number) => string;
	viewInFile: string;
	viewInFileWorkInProgress: string;
	viewOnPR: string;
};
