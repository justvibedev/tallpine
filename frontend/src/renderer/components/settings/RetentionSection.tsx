import { useState } from "react";
import { useTranslation } from "react-i18next";
import { isPolicyFieldVisible, useAgentPolicy, useUpdateAgentPolicy } from "../../hooks/useAgentPolicy";
import { type RetentionCategory, useRetentionRun } from "../../hooks/useRetentionRun";
import { ConfirmDialog } from "../ConfirmDialog";
import { Button } from "../ui/button";
import { AgentPolicyFieldRow } from "./AgentPolicyFieldRow";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";
import { formatBytes } from "@tallpine/shared/format/bytes";

/** The agent-policy rule this section edits; the generic policy section skips it. */
export const RETENTION_RULE = "sessionRetention";

/** Raw-event deletion has one control: the provider-event retention days
 * under Storage. Its policy field stays readable for the CLI and managed file. */
const RETENTION_HIDDEN_FIELD = "dropProviderEvents";

/** Items listed in the preview; the report carries at most 50. */
const PREVIEW_ITEMS = 20;

/**
 * Settings > Storage > Session retention: the sessionRetention policy fields
 * (locked rows show who locks them), a dry-run preview of what would go and
 * the space freed, and a confirmed "Run cleanup now".
 */
export function RetentionSection() {
	const { t } = useTranslation();
	const { policy, isLoading } = useAgentPolicy();
	const { update, saving, error: saveError } = useUpdateAgentPolicy();
	const { run, pending, error: runError, reset } = useRetentionRun();
	const [preview, setPreview] = useState<RetentionCategory | null>(null);
	const [freed, setFreed] = useState<RetentionCategory | null>(null);
	const [confirming, setConfirming] = useState(false);
	const rule = policy?.rules.find((r) => r.name === RETENTION_RULE);
	if (!rule) return null;
	const enabled = rule.fields.find((f) => f.name === "enabled")?.value === true;

	const startPreview = async () => {
		setFreed(null);
		try {
			setPreview((await run(true)) ?? null);
		} catch {
			// Renders from the mutation error.
		}
	};
	const runNow = async () => {
		try {
			setFreed((await run(false)) ?? null);
			setPreview(null);
			setConfirming(false);
		} catch {
			// The dialog shows the error.
		}
	};
	const items = (preview?.items ?? []).filter((i) => i.status === "would_free");
	const wouldFree = preview?.wouldFreeBytes ?? 0;

	return (
		<SettingsSection title={t("settings.retention.title")} sectionId="retention" grouped>
			{rule.fields
				.filter((field) => isPolicyFieldVisible(rule, field) && field.name !== RETENTION_HIDDEN_FIELD)
				.map((field) => (
					<AgentPolicyFieldRow
						key={field.name}
						field={field}
						disabled={isLoading || saving}
						onChange={(value) => {
							// A preview describes the old settings; Run needs a fresh one.
							setPreview(null);
							setFreed(null);
							update({ rule: rule.name, field: field.name, value });
						}}
					/>
				))}
			<SettingsRow
				label={t("settings.retention.preview")}
				description={
					<>
						{saveError ? <span role="alert" className="block text-error">{saveError}</span> : null}
						{runError && !confirming ? <span role="alert" className="block text-error">{runError}</span> : null}
						{freed ? (
							<span role="status" className="block">
								{t("settings.retention.freed", { size: formatBytes(freed.freedBytes) })}
								{freed.failed > 0 ? ` ${t("settings.retention.failed", { n: freed.failed })}` : ""}
							</span>
						) : null}
						{preview ? (
							<span role="status" className="block">
								{wouldFree > 0
									? t("settings.retention.wouldFree", { size: formatBytes(wouldFree), n: preview.wouldFree })
									: t("settings.retention.nothing")}
								{wouldFree > 0 && !enabled ? ` ${t("settings.retention.offNote")}` : ""}
							</span>
						) : null}
					</>
				}
			>
				<div className="flex items-center gap-2">
					<Button type="button" variant="footer" disabled={pending} onClick={startPreview}>
						{t("settings.retention.previewAction")}
					</Button>
					<Button
						type="button"
						variant="footer-primary"
						disabled={pending || !enabled || !preview || wouldFree === 0}
						title={!enabled ? t("settings.retention.runNeedsEnabled") : !preview ? t("settings.retention.runNeedsPreview") : undefined}
						onClick={() => {
							reset();
							setConfirming(true);
						}}
					>
						{t("settings.retention.run")}
					</Button>
				</div>
			</SettingsRow>
			{items.length > 0 ? (
				<ul aria-label={t("settings.retention.itemsLabel")} className="px-(--settings-row-inset) py-2">
					{items.slice(0, PREVIEW_ITEMS).map((item, i) => (
						<li key={`${item.sessionId}-${item.what}-${i}`} className="flex items-baseline justify-between gap-3 py-0.5 text-caption">
							<span className="min-w-0 truncate text-settings-label" title={item.path || item.sessionId}>
								<span className="font-mono">{item.sessionId}</span>
								<span className="text-settings-muted"> · {item.what}</span>
								{item.reason ? <span className="text-settings-muted"> · {item.reason}</span> : null}
							</span>
							<span className="shrink-0 tabular-nums text-settings-muted">{formatBytes(item.bytes)}</span>
						</li>
					))}
					{items.length > PREVIEW_ITEMS || preview?.itemsTruncated ? (
						<li className="py-0.5 text-caption text-settings-muted">
							{t("settings.retention.more", { n: (preview?.wouldFree ?? items.length) - Math.min(items.length, PREVIEW_ITEMS) })}
						</li>
					) : null}
				</ul>
			) : null}
			<ConfirmDialog
				open={confirming}
				destructive
				title={t("settings.retention.confirmTitle")}
				description={t("settings.retention.confirmDescription", { size: formatBytes(wouldFree), n: preview?.wouldFree ?? 0 })}
				confirmLabel={t("settings.retention.confirm")}
				busy={pending}
				error={runError}
				onConfirm={() => void runNow()}
				onOpenChange={(open) => setConfirming(open)}
			/>
		</SettingsSection>
	);
}
