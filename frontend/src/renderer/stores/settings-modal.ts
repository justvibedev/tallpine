import type { ProjectSettingsSection as ProjectFormSection } from "../components/ProjectSettingsForm";

/** The pages and shapes of the global and project Settings modal. */
export type GlobalSettingsSection =
	| "general"
	| "harness"
	| "reviews"
	| "security"
	| "agents"
	| "connections"
	| "shortcuts"
	| "browserProfiles"
	| "storage"
	| "diagnostics"
	| "help";

/** Project settings pages: the project form sections plus the cues manager. */
export type ProjectSettingsSection = ProjectFormSection | "environment" | "scripts" | "skills" | "cues";

/** Which failure a "Show diagnostics" shortcut is about: the job it produced,
 * if any, and the error the user saw. */
export type HarnessDiagnosticsRequest = { jobStartedAt?: string; error?: string };

export type SettingsModal =
	| {
			scope: "global";
			section?: GlobalSettingsSection;
			focusAgentId?: string;
			hostId?: string;
			/** Start focusAgentId's login flow as soon as the Harness page can. */
			startLogin?: boolean;
			/** Open the diagnostics of focusAgentId's failed install or update, ready to copy. */
			showDiagnostics?: HarnessDiagnosticsRequest;
			/** Preserve the project form while global recovery settings is above it. */
			returnTo?: Extract<SettingsModal, { scope: "project" }>;
	}
	| {
			scope: "project";
			projectId: string;
			hostId?: string;
			/** Page to open on, so callers can deep-link a project setting. */
			section?: ProjectSettingsSection;
	};
