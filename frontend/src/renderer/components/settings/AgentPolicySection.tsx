import { useTranslation } from "react-i18next";
import { type AgentPolicyRule, isPolicyFieldVisible, useAgentPolicy, useUpdateAgentPolicy } from "../../hooks/useAgentPolicy";
import { ACCOUNT_POOL_RULE } from "./AccountPoolSection";
import { AgentPolicyFieldRow } from "./AgentPolicyFieldRow";
import { RETENTION_RULE } from "./RetentionSection";

import { SecurityModeChoice } from "./SecurityModeChoice";
import { customProtections, DEV_MODE_RULE, modeWrites, shownMode } from "./securityModes";
import { SECURITY_MODE_RULE } from "./securityModeRule";
import { SettingsSection } from "./SettingsSection";

const ORCHESTRATOR_RULES: ReadonlySet<string> = new Set(["orchestratorApprovals", "crossProjectOrchestration"]);
/** Planner locks sit under Agents > Planning, not in the general policy list. */
const PLANNER_RULE = "planner";
const LOAD_RULES: ReadonlySet<string> = new Set(["repoAgentConfig"]);
/** Rules whose rows the Security tab renders itself; the protections are not editable here. */
/** Rules that belong to the Security tab; every other rule stays on the Harness page. */
export const SECURITY_RULES: ReadonlySet<string> = new Set([
	SECURITY_MODE_RULE,
	DEV_MODE_RULE,
	"protectState",
	"envFilter",
	"githubCreds",
	"permissionCap",
	...ORCHESTRATOR_RULES,
	...LOAD_RULES,
]);

/**
 * Enterprise agent policy: one section, one row per setting of every rule the
 * daemon reports. The rows, their labels, their kinds and which layer locks them
 * all come from the daemon's rule metadata.
 */
/** Planner fields the Reviews tab shows as app defaults instead of policy rows. */
const PLANNER_DEFAULT_FIELDS = new Set(["mode", "autoApprove"]);

export function AgentPolicySection({ scope = "general", children }: { scope?: "general" | "security" | "planning"; children?: React.ReactNode }) {
	const { t } = useTranslation();
	const { policy, isLoading } = useAgentPolicy();
	const { update, updateRules, saving, error } = useUpdateAgentPolicy();
	if (!policy) return null;
	const security = scope === "security";
	const planning = scope === "planning";
	const unrestricted = security && shownMode(policy) === "unrestricted";
	const custom = security ? customProtections(policy) : [];
	// Account pool and session retention have their own sections; the mode rules
	// are written through SecurityModeChoice, never as rows.
	const rows = (match: (rule: AgentPolicyRule) => boolean, forceDisabled: boolean) =>
		policy.rules
			.filter((rule) => rule.name !== ACCOUNT_POOL_RULE && rule.name !== RETENTION_RULE && rule.name !== SECURITY_MODE_RULE && rule.name !== DEV_MODE_RULE)
			.filter(match)
			.flatMap((rule) =>
				rule.fields
					.filter((field) => isPolicyFieldVisible(rule, field))
					// Mode and auto-approve are managed-only now; the app defaults rows show them (and their lock).
					.filter((field) => !(rule.name === PLANNER_RULE && PLANNER_DEFAULT_FIELDS.has(field.name)))
					.map((field) => (
						<AgentPolicyFieldRow
							key={`${rule.name}.${field.name}`}
							field={field}
							disabled={isLoading || saving || forceDisabled}
							siblingHarness={field.sourceField ? String(rule.fields.find((f) => f.name === field.sourceField)?.value ?? "") : undefined}
							onChange={(value) => {
								// A field that picks from this one (a model of this harness) is stale once this changes.
								const dependents = rule.fields
									.filter((f) => f.sourceField === field.name && !f.locked && String(f.value ?? "") !== "" && value !== field.value)
									.map((f) => f.name);
								update({ rule: rule.name, field: field.name, value, alsoClear: dependents });
							}}
						/>
					)),
			);
	return (
		<SettingsSection title={security ? t("settings.security") : planning ? t("settings.planning.title") : t("settings.agentPolicy.title")} sectionId={planning ? "planning" : "agentPolicy"} grouped>
			{policy.profile && !planning ? (
				<div data-testid="agent-policy-profile" className="px-3 pt-2 pb-1 text-caption leading-4 text-settings-muted">
					<p className="font-medium">{t("settings.agentPolicy.profile.hardened")}</p>
					<p>{t("settings.agentPolicy.profile.relaunch")}</p>
				</div>
			) : null}
			{planning ? children : null}
			{security ? <SecurityModeChoice policy={policy} disabled={isLoading || saving} onChange={(mode) => updateRules(modeWrites(mode))} /> : null}
			{security ? (
				<>
					{custom.length > 0 ? (
						<p data-testid="security-custom-protections" className="px-3 pb-2 text-caption leading-4 text-settings-muted">
							{t("settings.security.customProtections", { list: custom.map((key) => t(key)).join(", ") })}
						</p>
					) : null}
					<SecurityGroup title={t("settings.security.orchestrators.title")} note={unrestricted ? t("settings.security.orchestrators.unrestricted") : undefined}>
						{rows((rule) => ORCHESTRATOR_RULES.has(rule.name), unrestricted)}
					</SecurityGroup>
					<SecurityGroup title={t("settings.security.load.title")}>{rows((rule) => LOAD_RULES.has(rule.name), false)}</SecurityGroup>
				</>
			) : (
				planning
					? rows((rule) => rule.name === PLANNER_RULE, false)
					: rows((rule) => !SECURITY_RULES.has(rule.name) && rule.name !== PLANNER_RULE, false)
			)}
			{policy.warnings.length > 0 && !planning ? (
				<p role="alert" className="px-3 pb-2 text-caption leading-4 text-error">
					{t("settings.agentPolicy.warning")} {policy.warnings.join("; ")}
				</p>
			) : null}
			{error ? (
				<p role="alert" className="px-3 pb-2 text-caption leading-4 text-error">
					{error}
				</p>
			) : null}
		</SettingsSection>
	);
}

function SecurityGroup({ title, note, children }: { title: string; note?: string; children: React.ReactNode }) {
	return (
		<div role="group" aria-label={title}>
			<h3 className="px-3 pt-3 pb-1 text-caption font-medium text-settings-muted">{title}</h3>
			{note ? <p className="px-3 pb-1 text-caption leading-4 text-settings-muted">{note}</p> : null}
			{children}
		</div>
	);
}
