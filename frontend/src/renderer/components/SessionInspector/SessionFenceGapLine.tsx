import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { apiClient } from "../../lib/api-client";
import { useUiStore } from "../../stores/ui-store";

/** The "not fenced" reason the launch recorded under protectState, if any. */
export function fenceGapOf(record: unknown): string | undefined {
	const rules = (record as { rules?: Record<string, { fenceGap?: unknown }> } | undefined)?.rules;
	const gap = rules?.protectState?.fenceGap;
	return typeof gap === "string" && gap !== "" ? gap : undefined;
}

/**
 * Session inspector line for a session Security mode wanted to fence but whose
 * agent or platform cannot be: shows why, and links to the project's Security
 * mode switch (the opt-out).
 */
export function SessionFenceGapLine({ sessionId, projectId }: { sessionId: string; projectId: string }) {
	const { t } = useTranslation();
	const openProjectSettings = useUiStore((s) => s.openProjectSettings);
	const { data } = useQuery({
		queryKey: ["session-agent-policy", sessionId],
		queryFn: async () => {
			const { data: body, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/agent-policy", { params: { path: { sessionId } } });
			if (error) throw error;
			return body;
		},
		retry: false,
	});
	const gap = fenceGapOf(data?.record);
	if (!gap) return null;
	return (
		<p data-testid="session-fence-gap" className="py-1 text-caption leading-4 text-muted-foreground">
			{t("settings.agentPolicy.securityMode.notFenced", { reason: gap })}{" "}
			<button type="button" className="underline hover:text-foreground" onClick={() => openProjectSettings(projectId)}>
				{t("settings.agentPolicy.securityMode.notFencedAction")}
			</button>
		</p>
	);
}
