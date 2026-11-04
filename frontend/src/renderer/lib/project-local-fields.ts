import type { components } from "../../api/schema";

type ProjectConfig = components["schemas"]["ProjectConfig"];

/** Project settings that used to be CLI-only; edited on the local project pages. */
export type ProjectLocalFieldsDraft = {
	/** One repo-relative path per line. */
	symlinks?: string;
	cloneWorktreeInclude?: boolean;
	verifyCommand?: string;
	agentRules?: string;
	agentRulesFile?: string;
	orchestratorRules?: string;
	/** "" means auto (the orchestrator decides). */
	planningMode?: string;
	/** "" defers to the daemon-wide default. */
	defaultSessionMode?: string;
	/** Digits only; "" or 0 never stops. */
	autoReviewMaxRounds?: string;
	approveIdenticalPatch?: boolean;
};

export const PLANNING_MODES = ["auto", "always", "off"] as const;

/** i18n key of each planning mode's label. */
export const PLANNING_MODE_LABEL_KEY = {
	auto: "settings.project.planningMode.auto",
	always: "settings.project.planningMode.always",
	off: "settings.project.planningMode.off",
} as const;

/** A planning mode string from the daemon, "auto" when it is none of the known ones. */
export function planningModeKey(mode: string): (typeof PLANNING_MODE_LABEL_KEY)[keyof typeof PLANNING_MODE_LABEL_KEY] {
	return PLANNING_MODE_LABEL_KEY[mode as keyof typeof PLANNING_MODE_LABEL_KEY] ?? PLANNING_MODE_LABEL_KEY.auto;
}
export const SESSION_MODES = ["chat", "tui"] as const;

const lines = (text: string | undefined): string[] => (text ?? "").split("\n").map((l) => l.trim()).filter(Boolean);
const text = (value: string | undefined): string | undefined => value?.trim() || undefined;

export function projectLocalFieldsDraft(config: ProjectConfig): ProjectLocalFieldsDraft {
	return {
		symlinks: (config.symlinks ?? []).join("\n"),
		cloneWorktreeInclude: config.cloneWorktreeInclude ?? false,
		verifyCommand: config.verifyCommand ?? "",
		agentRules: config.agentRules ?? "",
		agentRulesFile: config.agentRulesFile ?? "",
		orchestratorRules: config.orchestratorRules ?? "",
		planningMode: config.planningMode ?? "",
		defaultSessionMode: config.defaultSessionMode ?? "",
		autoReviewMaxRounds: config.autoReviewMaxRounds ? String(config.autoReviewMaxRounds) : "",
		approveIdenticalPatch: config.approveIdenticalPatch ?? false,
	};
}

/** The config fields a draft saves; an empty value clears the key so the default applies. */
export function projectLocalFieldsConfig(draft: ProjectLocalFieldsDraft): Pick<ProjectConfig, "symlinks" | "cloneWorktreeInclude" | "verifyCommand" | "agentRules" | "agentRulesFile" | "orchestratorRules" | "planningMode" | "defaultSessionMode" | "autoReviewMaxRounds" | "approveIdenticalPatch"> {
	const symlinks = lines(draft.symlinks);
	const rounds = Number.parseInt(draft.autoReviewMaxRounds ?? "", 10);
	return {
		symlinks: symlinks.length ? symlinks : undefined,
		cloneWorktreeInclude: draft.cloneWorktreeInclude || undefined,
		verifyCommand: text(draft.verifyCommand),
		agentRules: draft.agentRules?.trim() ? draft.agentRules : undefined,
		agentRulesFile: text(draft.agentRulesFile),
		orchestratorRules: draft.orchestratorRules?.trim() ? draft.orchestratorRules : undefined,
		planningMode: (draft.planningMode || undefined) as ProjectConfig["planningMode"],
		defaultSessionMode: (draft.defaultSessionMode || undefined) as ProjectConfig["defaultSessionMode"],
		autoReviewMaxRounds: rounds > 0 ? rounds : undefined,
		approveIdenticalPatch: draft.approveIdenticalPatch || undefined,
	};
}
