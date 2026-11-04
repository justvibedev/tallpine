import type { AgentPolicy, AgentPolicyField, AgentPolicyRules } from "../../hooks/useAgentPolicy";
import { SECURITY_MODE_RULE } from "./securityModeRule";

export const DEV_MODE_RULE = "devMode";

export type SecurityMode = "standard" | "protected" | "unrestricted";

const enabledField = (policy: AgentPolicy, rule: string): AgentPolicyField | undefined => policy.rules?.find((r) => r.name === rule)?.fields.find((f) => f.name === "enabled");

/**
 * The one mode the policy is in. "conflict" means Security mode and Dev mode are
 * both on (a stored or managed leftover): Dev mode wins on the daemon, so the
 * app is effectively unrestricted, but the tab asks the user to pick one.
 */
export function securityModeOf(policy: AgentPolicy): SecurityMode | "conflict" {
	const security = policy.securityMode?.enabled ?? enabledField(policy, SECURITY_MODE_RULE)?.value === true;
	const dev = enabledField(policy, DEV_MODE_RULE)?.value === true;
	if (policy.profile) return "protected";
	if (security && dev) return "conflict";
	if (dev) return "unrestricted";
	return security ? "protected" : "standard";
}

/** The mode shown as selected: a conflict reads as Unrestricted, as the daemon resolves it. */
export const shownMode = (policy: AgentPolicy): SecurityMode => {
	const mode = securityModeOf(policy);
	return mode === "conflict" ? "unrestricted" : mode;
};

/** One PATCH body per mode: the chosen switch on, the other reset to inherit. */
export function modeWrites(mode: SecurityMode): AgentPolicyRules {
	return {
		[SECURITY_MODE_RULE]: { enabled: mode === "protected" ? true : null },
		[DEV_MODE_RULE]: { enabled: mode === "unrestricted" ? true : null },
	};
}

/** True when a lock keeps the mode's switches from taking the values the mode needs. */
export function isModeBlocked(policy: AgentPolicy, mode: SecurityMode): boolean {
	const security = enabledField(policy, SECURITY_MODE_RULE);
	const dev = enabledField(policy, DEV_MODE_RULE);
	const securityLocked = (policy.securityMode?.locked ?? security?.locked ?? false) || !!policy.profile;
	const securityValue = policy.securityMode?.enabled ?? security?.value === true;
	const blocked = (locked: boolean, value: boolean, want: boolean) => locked && value !== want;
	return blocked(securityLocked, securityValue || !!policy.profile, mode === "protected") || blocked(dev?.locked ?? false, dev?.value === true, mode === "unrestricted");
}

/** The protections the removed rows controlled; listed read-only when set away from default outside the tab. */
export const HIDDEN_PROTECTIONS = [
	{ rule: "protectState", field: "mode", labelKey: "settings.security.protection.state" },
	{ rule: "envFilter", field: "mode", labelKey: "settings.security.protection.env" },
	{ rule: "githubCreds", field: "mode", labelKey: "settings.security.protection.github" },
	{ rule: "permissionCap", field: "max", labelKey: "settings.security.protection.permission" },
] as const;

/** Hidden protections that a non-default value from the config file, CLI or managed file moved. */
export function customProtections(policy: AgentPolicy): (typeof HIDDEN_PROTECTIONS)[number]["labelKey"][] {
	return HIDDEN_PROTECTIONS.filter(({ rule, field }) => {
		const f = policy.rules?.find((r) => r.name === rule)?.fields.find((x) => x.name === field);
		return f && f.source !== "default" && f.source !== "security_mode" && f.source !== "managed_profile" && JSON.stringify(f.value) !== JSON.stringify(f.default);
	}).map((p) => p.labelKey);
}
