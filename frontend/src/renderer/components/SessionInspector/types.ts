import type { components } from "../../../api/schema";

export type ProjectConfig = components["schemas"]["ProjectConfig"];
export type OpenReviewerTerminal = (target: { handleId: string; harness: string }) => void;
export type ReviewerSurface = components["schemas"]["DomainReviewerSurface"];

