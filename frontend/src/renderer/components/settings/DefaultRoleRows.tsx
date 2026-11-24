import { useTranslation } from "react-i18next";
import { useAgentReadinessQuery } from "../../hooks/useAgentReadinessQuery";
import { useSettings, type RoleDefaults } from "../../hooks/useSettings";
import { useUpdateWorkflowDefaults } from "../../hooks/useUpdateWorkflowDefaults";
import { AGENT_OPTIONS, agentLabel } from "../../lib/agent-options";
import { DEFAULT_AGENT_PRIORITY_RANK, buildRankedAgentOptions, isLaunchableAgent, unknownAgentReadiness } from "../../lib/agent-select-options";
import { AgentModelField } from "./ProjectAgentRoleControls";
import { RoleDefaultApplyRow } from "./RoleDefaultApplyRow";
import { SettingsOptionMenu } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";

type Role = "worker" | "orchestrator";

// The agent a new project gets until the owner changes it (the daemon's
// built-in role default, used when that agent is installed and signed in).
const BUILTIN_ROLE_AGENT = "claude-code";

/**
 * Settings, Reviews: the agent, model and effort a NEW project starts with for
 * its worker and orchestrator, copied into its config at creation. An existing
 * project that names no model for the role inherits the model and effort at
 * spawn on that agent (B847); "Apply to all projects" clears or replaces the
 * overrides of the ones that differ. Empty keeps the harness default.
 */
export function DefaultRoleRows() {
	return (
		<>
			<DefaultRoleRow role="orchestrator" />
			<DefaultRoleRow role="worker" />
		</>
	);
}

function DefaultRoleRow({ role }: { role: Role }) {
	const { t } = useTranslation();
	const { settings, isLoading } = useSettings();
	const { update, saving } = useUpdateWorkflowDefaults();
	const agentsQuery = useAgentReadinessQuery();
	const defaults: RoleDefaults = settings?.workflowDefaults ?? {};
	const agent = defaults[`${role}Agent`] ?? "";
	const model = defaults[`${role}Model`] ?? "";
	const effort = defaults[`${role}Effort`] ?? "";
	const disabled = isLoading || saving || !settings;
	const options = buildRankedAgentOptions({
		agents: agentsQuery.data?.agents,
		priorityRank: DEFAULT_AGENT_PRIORITY_RANK,
		fallbackAgents: AGENT_OPTIONS.map((id) => unknownAgentReadiness(id, agentLabel(id))),
	})
		.filter((option) => isLaunchableAgent(option) || option.id === agent)
		.map((option) => ({ value: option.id, label: option.id === BUILTIN_ROLE_AGENT ? t("settings.reviews.defaultRole.builtinDefault", { label: option.label }) : option.label }));
	return (
		<>
			<SettingsRow label={t(`settings.reviews.defaultRole.${role}Agent.label`)} description={t(`settings.reviews.defaultRole.${role}Agent.description`)}>
				<SettingsOptionMenu
					aria-label={t(`settings.reviews.defaultRole.${role}Agent.label`)}
					value={agent}
					options={[{ value: "", label: t("settings.reviews.defaultRole.agentDefault") }, ...options]}
					triggerClassName="w-fit"
					disabled={disabled}
					// The daemon drops the old model and effort when the agent really changes.
					onChange={(next) => next !== agent && update({ [`${role}Agent`]: next })}
				/>
			</SettingsRow>
			{agent !== "" && (
				<SettingsRow label={t(`settings.reviews.defaultRole.${role}Model.label`)}>
					<div className="min-w-0">
						<AgentModelField
							role={role}
							ariaLabel={t(`settings.reviews.defaultRole.${role}Model.label`)}
							agentId={agent}
							projectId=""
							model={model}
							mode=""
							effort={effort}
							onModelChange={(next) => update({ [`${role}Model`]: next })}
							onModeChange={() => undefined}
							onEffortChange={(next) => update({ [`${role}Effort`]: next })}
							onValidityChange={() => undefined}
						/>
					</div>
				</SettingsRow>
			)}
			{agent !== "" && <RoleDefaultApplyRow role={role} disabled={disabled} />}
		</>
	);
}
