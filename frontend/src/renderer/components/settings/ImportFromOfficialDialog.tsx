import { useQueryClient } from "@tanstack/react-query";
import { CircleAlert, CircleCheck, LoaderCircle, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { workspaceQueryKey } from "../../hooks/useWorkspaceQuery";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogDescription,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "../ui/dialog";
import { Spinner } from "../ui/spinner";

type Report = components["schemas"]["AOImportReport"];
type Plan = components["schemas"]["AOImportProjectPlan"];
type View = "loading" | "list" | "importing" | "result";

/** Rows the user can tick: the import would write them. */
function selectable(plan: Plan) {
	return plan.selected && (plan.action === "create" || plan.action === "update");
}

/** New projects start ticked; changed ones wait for an explicit choice so a
 * rerun never silently overwrites edits made here. */
function defaultSelection(report: Report): Set<string> {
	return new Set(report.projects.filter((plan) => plan.action === "create" && plan.selected).map((plan) => plan.id));
}

/**
 * Detects an official Agent Orchestrator install (read-only) and imports the
 * chosen projects. Detection is a dry run; nothing is written until Import.
 */
export function ImportFromOfficialDialog({
	open,
	onOpenChange,
	onImported,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onImported?: () => void;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [view, setView] = useState<View>("loading");
	const [report, setReport] = useState<Report | null>(null);
	const [result, setResult] = useState<Report | null>(null);
	const [selected, setSelected] = useState<Set<string>>(new Set());
	const [includeSettings, setIncludeSettings] = useState(false);
	const [error, setError] = useState("");
	const request = useRef(0);

	const detect = useCallback(async () => {
		const id = ++request.current;
		setView("loading");
		setError("");
		const { data, error: failure } = await apiClient.POST("/api/v1/imports/ao", { body: { dryRun: true } });
		if (id !== request.current) return;
		if (failure || !data) {
			setReport(null);
			setError(apiErrorMessage(failure, t("importAo.failed")));
			setView("list");
			return;
		}
		setReport(data.report);
		setSelected(defaultSelection(data.report));
		setIncludeSettings(false);
		setView("list");
	}, [t]);

	useEffect(() => {
		if (open) void detect();
		else request.current += 1;
	}, [open, detect]);

	const hasSettingChanges = report?.settings?.some((setting) => setting.action === "update") ?? false;

	const run = async () => {
		if (selected.size === 0 && !(includeSettings && hasSettingChanges)) return;
		setView("importing");
		setError("");
		const { data, error: failure } = await apiClient.POST("/api/v1/imports/ao", {
			body: { dryRun: false, projectIds: [...selected], includeSettings },
		});
		if (failure || !data) {
			setError(apiErrorMessage(failure, t("importAo.failed")));
			setView("list");
			return;
		}
		setResult(data.report);
		setView("result");
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		onImported?.();
	};

	const canClose = view !== "importing";
	const toggle = (id: string, checked: boolean) =>
		setSelected((current) => {
			const next = new Set(current);
			if (checked) next.add(id);
			else next.delete(id);
			return next;
		});

	return (
		<Dialog open={open} onOpenChange={(next) => canClose && onOpenChange(next)}>
			<DialogContent className={settingsDialogContentClass} showCloseButton={false}>
				<DialogClose asChild>
					<button
						aria-label={t("common.close")}
						className="settings-dialog-close-button settings-close-button"
						disabled={!canClose}
						type="button"
					>
						<X aria-hidden="true" className="size-5" />
					</button>
				</DialogClose>
				<div className={settingsDialogHeaderClass}>
					<DialogTitle className="settings-dialog-title">{t("importAo.title")}</DialogTitle>
					<DialogDescription>{t("importAo.description")}</DialogDescription>
				</div>

				<div className={settingsDialogBodyClass}>
					{error ? <p className="text-sm text-destructive" role="alert">{error}</p> : null}
					{view === "loading" || view === "importing" ? (
						<div className="flex items-center gap-2 py-4 text-sm text-muted-foreground" role="status">
							<Spinner icon={LoaderCircle} className="size-4" />
							{t(view === "loading" ? "importAo.detecting" : "importAo.importing")}
						</div>
					) : null}
					{view === "list" && report && !report.found ? (
						<p className="text-sm text-muted-foreground">{t("importAo.notFound")}</p>
					) : null}
					{view === "list" && report?.found ? (
						<>
							<ProjectList onToggle={toggle} projects={report.projects} selected={selected} />
							{hasSettingChanges ? (
								<label className="flex cursor-pointer items-center gap-3 text-sm">
									<Checkbox checked={includeSettings} onCheckedChange={(value) => setIncludeSettings(value === true)} />
									{t("importAo.includeSettings")}
								</label>
							) : null}
							<p className="text-xs text-muted-foreground">{t("importAo.notImported")}</p>
						</>
					) : null}
					{view === "result" && result ? <ResultList report={result} /> : null}
				</div>

				<div className={settingsDialogFooterClass}>
					<div className="flex-1" />
					{view !== "importing" ? (
						<DialogClose asChild>
							<Button type="button" variant="footer">
								{t(view === "result" ? "settings.browserImport.done" : "importAo.cancel")}
							</Button>
						</DialogClose>
					) : null}
					{view === "list" && report?.found ? (
						<Button
							disabled={selected.size === 0 && !(includeSettings && hasSettingChanges)}
							onClick={() => void run()}
							type="button"
							variant="footer-primary"
						>
							{t("importAo.import")}
						</Button>
					) : null}
				</div>
			</DialogContent>
		</Dialog>
	);
}

function ProjectList({
	projects,
	selected,
	onToggle,
}: {
	projects: Plan[];
	selected: Set<string>;
	onToggle: (id: string, checked: boolean) => void;
}) {
	const { t } = useTranslation();
	if (projects.length === 0) return <p className="text-sm text-muted-foreground">{t("importAo.empty")}</p>;
	return (
		<ul className="flex flex-col divide-y divide-border rounded-md bg-card">
			{projects.map((plan) => {
				const canTick = selectable(plan);
				const detail = plan.error ?? plan.reason;
				return (
					<li key={plan.id}>
						<label className={`flex items-start gap-3 px-3 py-2.5 ${canTick ? "cursor-pointer" : "opacity-70"}`}>
							<Checkbox
								aria-label={plan.name}
								checked={canTick ? selected.has(plan.id) : plan.action === "unchanged"}
								className="mt-0.5"
								disabled={!canTick}
								onCheckedChange={(value) => onToggle(plan.id, value === true)}
							/>
							<span className="min-w-0 flex-1">
								<span className="flex items-baseline gap-2">
									<span className="truncate text-sm text-foreground">{plan.name}</span>
									<span className="ml-auto shrink-0 text-xs text-muted-foreground">{t(`importAo.status.${plan.action}`, { defaultValue: plan.action })}</span>
								</span>
								<span className="block truncate font-mono text-xs text-muted-foreground" title={plan.path}>{plan.path}</span>
								{detail ? <span className="block text-xs text-muted-foreground">{detail}</span> : null}
								{canTick ? (plan.warnings ?? []).map((warning) => (
									<span className="block text-xs text-muted-foreground" key={warning}>{warning}</span>
								)) : null}
							</span>
						</label>
					</li>
				);
			})}
		</ul>
	);
}

function ResultList({ report }: { report: Report }) {
	const { t } = useTranslation();
	const done = report.projects.filter((plan) => plan.applied);
	const failed = report.projects.filter((plan) => plan.selected && (plan.action === "error" || plan.action === "conflict"));
	return (
		<div className="flex flex-col gap-3">
			<p className="text-sm text-foreground" role="status">
				{t("importAo.result", { created: report.counts.create, updated: report.counts.update })}
			</p>
			<ul className="flex flex-col gap-1.5">
				{done.map((plan) => (
					<li className="flex items-center gap-2 text-sm" key={plan.id}>
						<CircleCheck aria-hidden="true" className="size-4 shrink-0 text-success" />
						<span className="truncate">{plan.name}</span>
					</li>
				))}
				{failed.map((plan) => (
					<li className="flex items-start gap-2 text-sm" key={plan.id}>
						<CircleAlert aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-destructive" />
						<span className="min-w-0">
							<span className="block truncate">{plan.name}</span>
							<span className="block text-xs text-muted-foreground">{plan.error ?? plan.reason}</span>
						</span>
					</li>
				))}
			</ul>
		</div>
	);
}
