import { AlertTriangle } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useAgentPolicy, useUpdateAgentPolicy } from "../../hooks/useAgentPolicy";
import { isWindowsPlatform } from "../../lib/platform";
import { securityModeOf } from "./securityModes";
import { Switch } from "../ui/switch";
import { AgentPolicyLock } from "./AgentPolicyLock";

import { SECURITY_MODE_ENV, SECURITY_MODE_RULE } from "./securityModeRule";
import { SettingsOptionMenu } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";

type Choice = "inherit" | "on" | "off";

/**
 * Project settings > General > Security mode: inherit the install's choice, or
 * force it on or off for this project (a project's off beats the install's on).
 * Also the Docker opt-in, with its warning. Writes the project layer; a null
 * value resets the project to inherit.
 */
export function ProjectSecurityModeSwitch({ projectId }: { projectId: string }) {
	const { t } = useTranslation();
	const { policy, isLoading } = useAgentPolicy(projectId);
	const { policy: appPolicy } = useAgentPolicy();
	const unrestricted = appPolicy ? ["unrestricted", "conflict"].includes(securityModeOf(appPolicy)) : false;
	const { update, saving, error } = useUpdateAgentPolicy(projectId);
	const fields = policy?.rules?.find((r) => r.name === SECURITY_MODE_RULE)?.fields;
	const enabled = fields?.find((f) => f.name === "enabled");
	const docker = fields?.find((f) => f.name === "allowDocker");
	if (!enabled) return null;
	const choice: Choice = enabled.source === "project" ? (enabled.value === true ? "on" : "off") : "inherit";
	const busy = isLoading || saving;
	const unavailable = isWindowsPlatform();
	const set = (value: Choice) => update({ rule: SECURITY_MODE_RULE, field: "enabled", value: value === "inherit" ? null : value === "on" });
	return (
		<div data-testid="project-security-mode">
			<SettingsRow label={t("settings.agentPolicy.securityMode.label")} description={t("settings.agentPolicy.securityMode.projectDescription")}>
				<div className="flex items-center gap-2">
					<AgentPolicyLock source={enabled.source} envVar={SECURITY_MODE_ENV} />
					<SettingsOptionMenu<Choice>
						aria-label={t("settings.agentPolicy.securityMode.label")}
						value={choice}
						disabled={busy || enabled.locked || unrestricted}
						onChange={set}
						options={[
							{ value: "inherit", label: t("settings.agentPolicy.securityMode.inherit") },
							{ value: "on", label: t("settings.agentPolicy.securityMode.on"), disabled: unavailable },
							{ value: "off", label: t("settings.agentPolicy.securityMode.off") },
						]}
					/>
				</div>
			</SettingsRow>
			{unavailable ? (
				<p data-testid="project-security-unavailable" role="status" className="px-3 pb-2 text-caption leading-4 text-settings-muted">
					{t("settings.security.mode.unavailableWindows", "Not available on Windows")}
				</p>
			) : null}
			{unrestricted ? (
				<p data-testid="project-security-unrestricted" role="status" className="px-3 pb-2 text-caption leading-4 text-settings-muted">
					{t("settings.security.project.unrestricted")}
				</p>
			) : null}
			{docker ? (
				<SettingsRow label={t("settings.agentPolicy.securityMode.allowDocker")} description={t("settings.agentPolicy.securityMode.allowDockerDescription")}>
					<Switch
						aria-label={t("settings.agentPolicy.securityMode.allowDocker")}
						checked={docker.value === true}
						disabled={busy || docker.locked}
						onCheckedChange={(on) => update({ rule: SECURITY_MODE_RULE, field: "allowDocker", value: on ? true : null })}
					/>
				</SettingsRow>
			) : null}
			{docker?.value === true ? (
				<p role="alert" className="flex items-start gap-1.5 px-3 pb-2 text-caption leading-4 text-error">
					<AlertTriangle className="mt-px size-icon-sm shrink-0" aria-hidden="true" />
					{t("settings.agentPolicy.securityMode.dockerWarning")}
				</p>
			) : null}
			{error ? (
				<p role="alert" className="px-3 pb-2 text-caption leading-4 text-error">
					{error}
				</p>
			) : null}
		</div>
	);
}
