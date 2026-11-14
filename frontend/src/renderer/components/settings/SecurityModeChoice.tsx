import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { AgentPolicy } from "../../hooks/useAgentPolicy";
import { cn } from "@/lib/utils";
import { isWindowsPlatform } from "../../lib/platform";
import { ConfirmDialog } from "../ConfirmDialog";
import { AgentPolicyLock } from "./AgentPolicyLock";
import { DEV_MODE_RULE, isModeBlocked, type SecurityMode, securityModeOf, shownMode } from "./securityModes";
import { DEV_MODE_ENV, SECURITY_MODE_ENV, SECURITY_MODE_RULE } from "./securityModeRule";
import { SettingsRow } from "./SettingsRow";

const MODES: readonly SecurityMode[] = ["standard", "protected", "unrestricted"];

/**
 * The one security choice: Standard, Protected or Unrestricted. Each mode is a
 * pair of switch values written in one PATCH. A lock disables only the modes it
 * rules out, so a fully pinned policy (hardened, managed Security mode) is read-only.
 */
export function SecurityModeChoice({ policy, disabled, onChange }: { policy: AgentPolicy; disabled: boolean; onChange: (mode: SecurityMode) => void }) {
	const { t } = useTranslation();
	const [confirming, setConfirming] = useState(false);
	const current = shownMode(policy);
	// Security mode's fence is not built for Windows; the daemon refuses it too.
	const protectedUnavailable = isWindowsPlatform();
	const conflict = securityModeOf(policy) === "conflict";
	const fieldOf = (rule: string) => policy.rules.find((r) => r.name === rule)?.fields.find((f) => f.name === "enabled");
	const securityLocked = fieldOf(SECURITY_MODE_RULE)?.locked || !!policy.profile;
	const devField = fieldOf(DEV_MODE_RULE);
	// Name the variable of the field that is actually locked.
	const lockSource = securityLocked ? (fieldOf(SECURITY_MODE_RULE)?.source ?? "managed_profile") : devField?.locked ? devField.source : undefined;
	const lockEnv = securityLocked ? SECURITY_MODE_ENV : DEV_MODE_ENV;
	const choose = (mode: SecurityMode) => {
		if (mode === current && !conflict) return;
		if (mode === "unrestricted") setConfirming(true);
		else onChange(mode);
	};
	return (
		<div data-testid="security-mode-choice" className="px-3 py-2">
			<SettingsRow label={t("settings.security.mode.label")} description={t("settings.security.mode.description")}>
				<AgentPolicyLock source={lockSource} envVar={lockEnv} />
			</SettingsRow>
			<div role="radiogroup" aria-label={t("settings.security.mode.label")} className="flex flex-col gap-1.5 pt-1">
				{MODES.map((mode) => {
					const blocked = (isModeBlocked(policy, mode) && mode !== current) || (protectedUnavailable && mode === "protected");
					return (
						<button
							key={mode}
							type="button"
							role="radio"
							aria-checked={mode === current}
							disabled={disabled || blocked}
							onClick={() => choose(mode)}
							className={cn("rounded-md border px-3 py-2 text-left disabled:opacity-60", mode === current ? "border-accent bg-settings-hover" : "border-settings-border")}
						>
							<span className="block text-body font-medium">{t(`settings.security.mode.${mode}.label`)}</span>
							<span className="block text-caption leading-4 text-settings-muted">{t(`settings.security.mode.${mode}.line1`)}</span>
							<span className="block text-caption leading-4 text-settings-muted">{t(`settings.security.mode.${mode}.line2`)}</span>
							{protectedUnavailable && mode === "protected" ? (
								<span data-testid="security-mode-unavailable" className="block text-caption leading-4 text-warning">{t("settings.security.mode.unavailableWindows", "Not available on Windows")}</span>
							) : null}
						</button>
					);
				})}
			</div>
			{conflict ? (
				<p data-testid="security-mode-conflict" role="status" className="pt-2 text-caption leading-4 text-warning">
					{t("settings.security.mode.conflict")}
				</p>
			) : null}
			<ConfirmDialog
				open={confirming}
				title={t("settings.security.mode.unrestricted.confirmTitle")}
				description={t("settings.security.mode.unrestricted.confirmBody")}
				confirmLabel={t("settings.security.mode.unrestricted.confirm")}
				destructive
				onConfirm={() => {
					setConfirming(false);
					onChange("unrestricted");
				}}
				onOpenChange={setConfirming}
			/>
		</div>
	);
}
