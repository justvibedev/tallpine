import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "./api-client";
import { patchProjectConfig } from "./project-settings-api";

/**
 * Turns the automatic skills/instructions notice off ("Don't ask again for this
 * project") or back on. It is a project config flag, so it rides the ordinary
 * project settings update; the latest config is re-read so nothing else in it is
 * overwritten.
 */
export async function setSkillsOfferOff(projectId: string, off: boolean): Promise<void> {
	await patchProjectConfig(projectId, (config) => ({ ...config, skillsOfferOff: off || undefined }));
}

type Skills = components["schemas"]["ProjectSkillsResponse"];
export type SkillOffer = components["schemas"]["ProjectSkillOffer"];

export const projectSkillsQueryKey = (projectId: string) => ["project-skills", projectId] as const;

export async function fetchProjectSkills(projectId: string): Promise<Skills> {
	const { data, error } = await apiClient.GET("/api/v1/projects/{id}/skills", { params: { path: { id: projectId } } });
	if (error || !data) throw new Error(apiErrorMessage(error));
	return data;
}

/** Applies one offer; the single apply path for Settings > Skills and the review dialog. */
export async function applyProjectSkillOffer(projectId: string, offerId: string) {
	const { data, error } = await apiClient.POST("/api/v1/projects/{id}/skills/offers/{offerId}/apply", { params: { path: { id: projectId, offerId } } });
	if (error) throw new Error(apiErrorMessage(error));
	return data;
}

/** True when an automatic offer should be raised: enabled, and something to create or commit. */
export function skillsOfferWanted(skills: Skills | undefined): boolean {
	return skills !== undefined && skills.offersEnabled && (skills.offers.length > 0 || skills.pendingCommit.length > 0);
}

const HARNESS_NAMES: Record<string, string> = { "claude-code": "Claude Code", codex: "Codex" };

/** What the card line names: the harnesses that can't see something, the skills count, the instruction files. */
export function summarizeOffers(offers: readonly SkillOffer[]) {
	const agents = [...new Set(offers.map((o) => HARNESS_NAMES[o.harness] ?? o.harness))];
	const files = [...new Set(offers.filter((o) => o.id === "claude-md" || o.id === "codex-md").map((o) => (o.id === "claude-md" ? "AGENTS.md" : "CLAUDE.md")))];
	const skillNames = [...new Set(offers.filter((o) => o.id.endsWith("-skills")).flatMap((o) => o.items.map((i) => i.path.split("/").pop() ?? i.path)))].sort();
	return { agent: agents.join(" / "), file: files.join(" / "), skills: skillNames.length, skillNames };
}

/** A link's target as a project-root-relative path: the symlink keeps its relative target on disk, but people read "../../x" as leaving the project. */
export function projectRelativeTarget(path: string, target: string): string {
	const parts = path.split("/").slice(0, -1);
	for (const seg of target.split("/")) {
		if (seg === "..") parts.pop();
		else if (seg !== "." && seg !== "") parts.push(seg);
	}
	return parts.join("/");
}

/** One plain-language line for an offer item, paths relative to the project root. */
export function offerItemLine(item: SkillOffer["items"][number], t: (key: string, options?: Record<string, unknown>) => string): string {
	if (item.kind !== "link") return `${item.path}  ${item.content?.trim()}`;
	return t("skillsNotice.linkLine", { name: item.path.split("/").pop(), path: item.path, target: projectRelativeTarget(item.path, item.target ?? "") });
}

/** Uncommitted paths grouped for display: instruction files, skill names (one per skill folder) and anything else. */
export function groupPendingPaths(paths: readonly string[]) {
	const instructions: string[] = [];
	const skills = new Set<string>();
	const other: string[] = [];
	for (const path of paths) {
		const skill = /^(.*\/skills\/[^/]+)/.exec(path);
		if (skill) skills.add(skill[1]);
		else if (!path.includes("/")) instructions.push(path);
		else other.push(path);
	}
	return { instructions, skills: [...skills].map((dir) => dir.split("/").pop() ?? dir).filter((n, i, all) => all.indexOf(n) === i).sort(), other };
}

/** Every uncommitted name in one list, for the one-line notice: instruction files, then skills, then the rest. */
export function pendingNames(paths: readonly string[]): string {
	const { instructions, skills, other } = groupPendingPaths(paths);
	return [...instructions, ...skills, ...other].join(", ");
}
