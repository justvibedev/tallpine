import { useQuery } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { agentModelsQueryOptions } from "../../hooks/useAgentModelsQuery";
import { effectiveStrongerReviewer, type ReviewDefaultsDraft } from "../../lib/project-review-defaults";
import { AgentModelField } from "./ProjectAgentRoleControls";
import { SettingsRow } from "./SettingsRow";

/**
 * The reviewer a risky PR gets instead of the normal one: an agent, a model
 * from that agent's catalog and an effort. With nothing stored the built-in
 * Claude Code default is shown. A model that is stored but missing from the
 * catalog is flagged by the picker, and no model can be typed in free text
 * unless the agent's catalog declares custom entry.
 */
export function StrongerReviewerControls({ values, onChange, renderAgent, modelScope, modelHostId }: {
	values: ReviewDefaultsDraft;
	onChange: (patch: Partial<ReviewDefaultsDraft>) => void;
	/** The agent picker of the host app, restricted to reviewer agents. */
	renderAgent: (props: { value: string; onChange: (agent: string) => void }) => ReactNode;
	modelScope: (agent: string) => string;
	modelHostId?: string;
}) {
	const { t } = useTranslation();
	const [, setValid] = useState(true);
	const shown = effectiveStrongerReviewer(values);
	const catalog = useQuery(agentModelsQueryOptions(shown.harness, modelScope(shown.harness), modelHostId)).data;
	const unknownModel = shown.model !== "" && catalog?.models !== undefined && catalog.models.length > 0 && !catalog.models.some((model) => model.id === shown.model);
	// The first edit pins the whole shown reviewer, so a partial edit never leaves
	// the daemon to default the part the person was looking at.
	const pin = (patch: Partial<ReviewDefaultsDraft>) => onChange({ reviewRiskHarness: shown.harness, reviewRiskModel: shown.model, reviewRiskEffort: shown.effort, ...patch });
	return (
		<>
			<SettingsRow label={t("settings.reviewDefaults.strongerAgent")} description={t("settings.reviewDefaults.strongerReviewerDescription")}>
				<div className="min-w-0">{renderAgent({ value: shown.harness, onChange: (agent) => agent !== shown.harness && pin({ reviewRiskHarness: agent, reviewRiskModel: "", reviewRiskEffort: "" }) })}</div>
			</SettingsRow>
			<SettingsRow label={t("settings.reviewDefaults.strongerModel")} description={shown.builtIn ? t("settings.reviewDefaults.builtInReviewer") : undefined}>
				<div className="min-w-0">
					<AgentModelField
						role="reviewer"
						ariaLabel={t("settings.reviewDefaults.strongerModel")}
						agentId={shown.harness}
						projectId={modelScope(shown.harness)}
						hostId={modelHostId}
						model={shown.model}
						mode=""
						effort={shown.effort}
						onModelChange={(model) => pin({ reviewRiskModel: model })}
						onModeChange={() => undefined}
						onEffortChange={(effort) => pin({ reviewRiskEffort: effort })}
						onValidityChange={setValid}
					/>
					{unknownModel && <p role="alert" className="px-1 pt-1 text-xs leading-row text-warning">{t("settings.reviewDefaults.unknownModel", { model: shown.model })}</p>}
				</div>
			</SettingsRow>
		</>
	);
}
