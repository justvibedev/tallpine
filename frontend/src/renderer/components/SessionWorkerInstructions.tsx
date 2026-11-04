import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { clientForSessionHost } from "../lib/host-clients";
import type { WorkspaceSession } from "../types/workspace";

/** The project rules (agentRules plus agentRulesFile) this worker received as
 * standing instructions. Read-only and collapsed; absent when there are none. */
export function SessionWorkerInstructions({ hostId, session }: { hostId?: string; session: WorkspaceSession }) {
	const { t } = useTranslation();
	const query = useQuery({
		queryKey: ["session-worker-instructions", hostId ?? "", session.id],
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}", {
				params: { path: { sessionId: session.id } },
			});
			if (error) throw error;
			return data?.workerInstructions ?? "";
		},
	});
	const rules = query.data;
	if (!rules) return null;
	return (
		<details className="mt-3 border-t border-(--color-border-settings-input) pt-3" data-testid="worker-instructions">
			<summary className="cursor-pointer text-xs font-medium">{t("inspector.workerInstructions")}</summary>
			<pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap text-xs">{rules}</pre>
		</details>
	);
}
