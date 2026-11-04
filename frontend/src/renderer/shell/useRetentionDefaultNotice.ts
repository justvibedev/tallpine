import type { UseNavigateResult } from "@tanstack/react-router";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { useAgentPolicy } from "../hooks/useAgentPolicy";
import { useUiStore } from "../stores/ui-store";
import { RETENTION_RULE } from "../components/settings/RetentionSection";

const NOTICE_KEY = "tallpine.retentionDefaultNoticeSeen";

/**
 * One-time notice (B201) for an install whose session retention is on only
 * because the default now is: it never set the value, so tell it once, app
 * wide, before the daily cleanup pass first purges.
 */
export function useRetentionDefaultNotice(navigate: UseNavigateResult<string>) {
	const { t } = useTranslation();
	const { policy } = useAgentPolicy();
	const showGlobalToast = useUiStore((s) => s.showGlobalToast);
	const fields = policy?.rules.find((r) => r.name === RETENTION_RULE)?.fields;
	const enabled = fields?.find((f) => f.name === "enabled");
	const days = fields?.find((f) => f.name === "days")?.value;
	const show = enabled?.value === true && enabled.source === "default";
	useEffect(() => {
		if (!show) return;
		try {
			if (window.localStorage.getItem(NOTICE_KEY) === "1") return;
			window.localStorage.setItem(NOTICE_KEY, "1");
		} catch {
			// No storage: the notice may repeat on the next start.
		}
		showGlobalToast(t("settings.retention.defaultNotice", { days: typeof days === "number" ? days : 7 }), undefined, {
			dedupeKey: "retention-default-notice",
			durationMs: 30_000,
			action: { label: t("settings.retention.defaultNoticeAction"), onClick: () => void navigate({ to: "/settings" }) },
		});
	}, [show, days, showGlobalToast, navigate, t]);
}
