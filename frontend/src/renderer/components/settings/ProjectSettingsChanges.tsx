import { ProjectSettingsRow, ProjectSettingsSection } from "@tallpine/product-ui";
import { useTranslation } from "react-i18next";
import { type SettingsChange, useSettingsChanges } from "../../hooks/useSettingsChanges";
import { formatVersionTime } from "../ArtifactHistory";

/** Longest value shown before it is cut. */
const MAX_VALUE = 60;

function shortValue(json: string): string {
	const text = json === "null" ? "—" : json;
	return text.length > MAX_VALUE ? `${text.slice(0, MAX_VALUE - 1)}…` : text;
}

/** "You", or "Orchestrator app-orch" for an agent token ("orchestrator:app-orch"). */
export function settingsActorLabel(actor: string, t: (key: string, options?: Record<string, string>) => string): string {
	if (actor === "user") return t("settings.project.changes.you");
	const [role, session] = actor.split(":", 2);
	if (role === "orchestrator" && session) return t("settings.project.changes.orchestrator", { session });
	return actor;
}

/**
 * Project settings > General > Recent setting changes: the last changes to
 * this project's settings and who made them, so a change an orchestrator made
 * is visible. Hidden until something changed.
 */
export function ProjectSettingsChanges({ projectId }: { projectId: string }) {
	const { t } = useTranslation();
	const { data: changes } = useSettingsChanges(projectId);
	if (!changes || changes.length === 0) return null;
	return (
		<ProjectSettingsSection title={t("settings.project.changes.title")} grouped>
			{changes.map((change: SettingsChange) => (
				<ProjectSettingsRow key={change.id} label={change.key.replace(/^project\./, "")}>
					<div className="min-w-0 text-right">
						<div className="truncate font-mono text-xs" title={`${change.oldValue} → ${change.newValue}`}>
							{shortValue(change.oldValue)} → {shortValue(change.newValue)}
						</div>
						<div className="text-xs text-settings-muted">
							{settingsActorLabel(change.actor, t)} · {formatVersionTime(change.changedAt)}
						</div>
					</div>
				</ProjectSettingsRow>
			))}
		</ProjectSettingsSection>
	);
}
