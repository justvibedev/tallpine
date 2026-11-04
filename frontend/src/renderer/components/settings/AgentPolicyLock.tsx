import { Lock } from "lucide-react";
import { useTranslation } from "react-i18next";

/** Shows who locks an agent policy setting: the organization (managed config) or the environment. */
export function AgentPolicyLock({ source, envVar }: { source: string | undefined; envVar?: string }) {
	const { t } = useTranslation();
	if (source !== "managed" && source !== "managed_profile" && source !== "env") return null;
	return (
		<span data-testid="agent-policy-lock" className="flex items-center gap-1 text-caption text-settings-muted">
			<Lock className="size-icon-sm" aria-hidden="true" />
			{source === "managed" || source === "managed_profile" ? t("settings.agentPolicy.managed") : envVar ? t("settings.agentPolicy.envVar", { name: envVar }) : t("settings.agentPolicy.env")}
		</span>
	);
}
