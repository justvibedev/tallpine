import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { apiClient } from "../lib/api-client";
import { ImportFromOfficialDialog } from "./settings/ImportFromOfficialDialog";

/**
 * First-run entry: a quiet text link, shown only when the official Agent
 * Orchestrator is installed here and has projects this install does not.
 * Detection is a read-only dry run; any failure just hides the link.
 */
export function ImportFromOfficialLink() {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const [importable, setImportable] = useState(false);
	useEffect(() => {
		let cancelled = false;
		void (async () => {
			try {
				const { data } = await apiClient.POST("/api/v1/imports/ao", { body: { dryRun: true } });
				if (!cancelled) setImportable(data?.report.found === true && data.report.counts.create > 0);
			} catch {
				// No daemon yet, or an unreadable install: nothing to offer.
			}
		})();
		return () => {
			cancelled = true;
		};
	}, []);
	if (!importable) return null;
	return (
		<>
			<button
				className="inline-flex items-center border-b border-dashed border-transparent pb-px text-sm text-muted-foreground hover:border-current hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
				onClick={() => setOpen(true)}
				type="button"
			>
				{t("importAo.title")}
			</button>
			<ImportFromOfficialDialog onOpenChange={setOpen} open={open} />
		</>
	);
}
