import { useTranslation } from "react-i18next";
import { useAgentPolicy, useUpdateAgentPolicy } from "../../hooks/useAgentPolicy";
import { AgentPolicyFieldRow } from "./AgentPolicyFieldRow";
import { SettingsSection } from "./SettingsSection";

/** The agent-policy rule that holds the account pool settings. */
export const ACCOUNT_POOL_RULE = "accountPool";

/**
 * Where new sessions start across several signed-in accounts, and whether a
 * session moves to another account at a usage limit. Both are off by default.
 * The rows come from the daemon's rule metadata, so a managed profile or the
 * environment locks them like any other policy setting.
 */
export function AccountPoolSection() {
	const { t } = useTranslation();
	const { policy, isLoading } = useAgentPolicy();
	const { update, saving, error } = useUpdateAgentPolicy();
	const rule = policy?.rules.find((r) => r.name === ACCOUNT_POOL_RULE);
	if (!rule) return null;
	return (
		<SettingsSection title={t("settings.accountPool.title")} sectionId="accountPool" grouped>
			{rule.fields.map((field) => (
				<AgentPolicyFieldRow
					key={field.name}
					field={field}
					disabled={isLoading || saving}
					onChange={(value) => update({ rule: rule.name, field: field.name, value })}
				/>
			))}
			<p className="px-3 pb-2 text-caption leading-4 text-settings-muted">{t("settings.accountPool.note")}</p>
			{error ? (
				<p role="alert" className="px-3 pb-2 text-caption leading-4 text-error">
					{error}
				</p>
			) : null}
		</SettingsSection>
	);
}
