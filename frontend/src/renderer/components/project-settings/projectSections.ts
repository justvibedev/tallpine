import { Bot, GitBranch, GitPullRequest, MessageSquare, MonitorCog, Play, Settings2, Shield, Sparkles, Workflow, type LucideIcon } from "lucide-react";
import type { TFunction } from "i18next";
import type { ProjectSettingsSection } from "../../stores/settings-modal";

export type ProjectSectionEntry = { id: ProjectSettingsSection; label: string; icon: LucideIcon };

/** The project settings nav: remote hosts keep two pages; local projects get the regrouped list. */
export function projectSettingsSections(t: TFunction, remote: boolean, scratch = false): ProjectSectionEntry[] {
	if (remote) {
		return [
			{ id: "general", label: t("settings.project.general"), icon: MonitorCog },
			{ id: "agents", label: t("settings.project.agents"), icon: Bot },
		];
	}
	// A scratch project has no repository, so it has no PRs or reviews to configure.
	const sections: ProjectSectionEntry[] = [
		{ id: "details", label: t("settings.project.nav.details"), icon: MonitorCog },
		{ id: "repository", label: t("settings.project.nav.repository"), icon: GitBranch },
		{ id: "agents", label: t("settings.project.agents"), icon: Bot },
		{ id: "reviews", label: t("settings.project.nav.reviews"), icon: GitPullRequest },
		{ id: "workflow", label: t("settings.project.workflow"), icon: Workflow },
		{ id: "sessions", label: t("settings.sessions"), icon: MessageSquare },
		{ id: "security", label: t("settings.security"), icon: Shield },
		{ id: "advanced", label: t("settings.advanced"), icon: Settings2 },
		{ id: "skills", label: t("settings.project.skills"), icon: Sparkles },
		{ id: "cues", label: t("cues.title"), icon: Play },
	];
	return scratch ? sections.filter((s) => s.id !== "reviews") : sections;
}

/** Pages that were folded into the regrouped local project pages keep opening where their content went. */
export function initialProjectSection(section: ProjectSettingsSection | undefined, remote: boolean): ProjectSettingsSection {
	if (remote) return section ?? "general";
	if (!section || section === "general") return "details";
	return section === "scripts" ? "repository" : section === "environment" ? "advanced" : section;
}
