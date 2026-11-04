import { useTranslation } from "react-i18next";
import { useModelUsage } from "../../hooks/useModelUsage";
import { formatTokenCount } from "../../lib/format-token-count";

const tokens = (n: number) => formatTokenCount(n).replace(/ tok$/, "");

/** Cumulative tokens by model for the session; absent until a turn reported any. */
export function ModelUsageSection({ sessionId, hostId }: { sessionId: string; hostId?: string }) {
	const { t } = useTranslation();
	const { models } = useModelUsage(sessionId, hostId);
	if (models.length === 0) return null;
	const showCredits = models.some((m) => m.credits > 0);
	return (
		<div className="mt-3 border-t border-border pt-3">
			<p className="mb-1.5 font-medium">{t("contextMeter.usage.title")}</p>
			<table className="w-full tabular-nums">
				<thead>
					<tr className="text-muted-foreground">
						<th className="text-left font-normal">{t("contextMeter.usage.model")}</th>
						<th className="text-right font-normal">{t("contextMeter.usage.input")}</th>
						<th className="text-right font-normal">{t("contextMeter.usage.cacheWrite")}</th>
						<th className="text-right font-normal">{t("contextMeter.usage.cacheRead")}</th>
						<th className="text-right font-normal">{t("contextMeter.usage.output")}</th>
						{showCredits && <th className="text-right font-normal">{t("contextMeter.usage.credits")}</th>}
					</tr>
				</thead>
				<tbody>
					{models.map((m) => (
						<tr key={m.model}>
							<td className="max-w-[7rem] truncate" title={m.model}>{m.model}</td>
							<td className="text-right">{tokens(m.inputTokens)}</td>
							<td className="text-right">{tokens(m.cacheCreationTokens)}</td>
							<td className="text-right">{tokens(m.cacheReadTokens)}</td>
							<td className="text-right">{tokens(m.outputTokens)}</td>
							{showCredits && <td className="text-right">{m.credits.toFixed(2)}</td>}
						</tr>
					))}
				</tbody>
			</table>
		</div>
	);
}
