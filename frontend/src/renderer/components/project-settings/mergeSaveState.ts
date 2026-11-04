import type { ProjectSettingsSaveState } from "../ProjectSettingsForm";

const RANK: Record<ProjectSettingsSaveState["phase"], number> = { idle: 0, saved: 1, pending: 2, saving: 3, failed: 4 };

/** One state for a page that hosts several editors: dirty if any is, and the worst phase wins. */
export function mergeSaveStates(states: ProjectSettingsSaveState[]): ProjectSettingsSaveState {
	const worst = states.reduce((a, b) => (RANK[b.phase] > RANK[a.phase] ? b : a));
	const any = (key: "dirty" | "requestPending" | "unsaveable" | "draftFailed") => states.some((s) => s[key]);
	return { ...worst, dirty: any("dirty"), requestPending: any("requestPending"), unsaveable: any("unsaveable"), draftFailed: any("draftFailed"), replacementError: states.find((s) => s.replacementError)?.replacementError };
}

export const PROJECT_MAIN_FORM_ID = "project-settings-form";

/**
 * Submits only the editors that hold unsaved changes: the scripts and env editors rewrite the
 * whole config, so submitting a clean one could race and undo another editor's save.
 * With none dirty (e.g. a retry after a failure) the main form is submitted.
 */
export function submitDirtyProjectForms(main: ProjectSettingsSaveState, extra: ProjectSettingsSaveState, extraFormId: string | undefined) {
	const ids = [main.dirty && PROJECT_MAIN_FORM_ID, extra.dirty && extraFormId].filter((id): id is string => Boolean(id));
	for (const id of ids.length ? ids : [PROJECT_MAIN_FORM_ID]) (document.getElementById(id) as HTMLFormElement | null)?.requestSubmit();
}

export const extraFormIdFor = (section: string): string | undefined => section === "repository" ? "project-scripts-form" : section === "advanced" ? "project-environment-form" : undefined;
