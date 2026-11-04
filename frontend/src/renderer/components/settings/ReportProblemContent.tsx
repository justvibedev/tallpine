import { useTranslation } from "react-i18next";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import {
	collectReportProblemDiagnostics,
	formatReportProblemDraft,
	reportProblemDestinationUrl,
	type ReportProblemDiagnostics,
	type ReportProblemOutput,
} from "../../lib/report-problem";
import { tallpineBridge } from "../../lib/bridge";
import { Button } from "../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

type DestinationIconProps = {
	className?: string;
};

function GithubIcon({ className }: DestinationIconProps) {
	return (
		<svg className={className} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
			<path d="M12 .5C5.65.5.5 5.65.5 12c0 5.08 3.29 9.38 7.86 10.9.58.1.79-.25.79-.56v-2.15c-3.2.7-3.88-1.37-3.88-1.37-.52-1.34-1.28-1.7-1.28-1.7-1.05-.72.08-.7.08-.7 1.16.08 1.77 1.2 1.77 1.2 1.03 1.76 2.7 1.25 3.36.96.1-.75.4-1.25.73-1.54-2.56-.29-5.26-1.28-5.26-5.7 0-1.26.45-2.29 1.19-3.1-.12-.3-.52-1.47.11-3.05 0 0 .97-.31 3.18 1.18A10.96 10.96 0 0 1 12 5.99c.98 0 1.97.13 2.9.38 2.2-1.49 3.17-1.18 3.17-1.18.63 1.58.23 2.75.11 3.05.74.81 1.19 1.84 1.19 3.1 0 4.43-2.7 5.4-5.27 5.69.41.36.78 1.07.78 2.16v3.2c0 .31.21.67.8.55A11.51 11.51 0 0 0 23.5 12C23.5 5.65 18.35.5 12 .5Z" />
		</svg>
	);
}

const DEFAULT_DIAGNOSTICS: ReportProblemDiagnostics = {
	appVersion: "unknown",
	buildMode: "unknown",
	daemonState: "unknown",
	generatedAt: "unknown",
	platform: "unknown",
	routeSurface: "unknown",
};

type DestinationOption = {
	value: ReportProblemOutput;
	label: string;
	action: string;
	icon: (props: DestinationIconProps) => ReactNode;
};

export function ReportProblemContent({ active }: { active: boolean }) {
	const { t } = useTranslation();
	const destinations: DestinationOption[] = [
		{ value: "github", label: t("report.github"), action: t("report.githubAction"), icon: GithubIcon },
	];
	const titleId = useId();
	const detailsId = useId();
	const titleRef = useRef<HTMLInputElement>(null);
	const [summary, setSummary] = useState("");
	const [details, setDetails] = useState("");
	const [copiedOutput, setCopiedOutput] = useState<ReportProblemOutput | null>(null);
	const [copyError, setCopyError] = useState<string | null>(null);
	const [diagnostics, setDiagnostics] = useState<ReportProblemDiagnostics>(DEFAULT_DIAGNOSTICS);

	const copiedLabel = destinations.find((option) => option.value === copiedOutput)?.label;

	useEffect(() => {
		if (!active) {
			setSummary("");
			setDetails("");
			setCopiedOutput(null);
			setCopyError(null);
			return;
		}
		let cancelled = false;
		void collectReportProblemDiagnostics().then((nextDiagnostics) => {
			if (!cancelled) setDiagnostics(nextDiagnostics);
		});
		return () => {
			cancelled = true;
		};
	}, [active]);

	const input = { summary, details };
	const canSubmit = summary.trim().length > 0 && details.trim().length > 0;

	const clearStatus = () => {
		setCopiedOutput(null);
		setCopyError(null);
	};

	const copyDraft = async (output: ReportProblemOutput) => {
		if (!canSubmit) return;
		setCopyError(null);
		const draft = formatReportProblemDraft(input, diagnostics, output);
		try {
			await tallpineBridge.clipboard.writeText(draft);
			const destinationUrl = reportProblemDestinationUrl(input, diagnostics, output);
			if (destinationUrl) {
				await tallpineBridge.app.openExternal(destinationUrl);
			}
			setCopiedOutput(output);
			setSummary("");
			setDetails("");
		} catch (err) {
			setCopyError(err instanceof Error ? err.message : t("report.copyFailed"));
			setCopiedOutput(null);
		}
	};

	return (
		<div
			className="flex flex-col gap-4"
			onKeyDown={(event) => {
				if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
					event.preventDefault();
					void copyDraft("github");
				}
			}}
		>
			<div className="flex flex-col gap-1.5">
				<label className="settings-field-label" htmlFor={titleId}>
					{t("report.titleLabel")}
				</label>
				<input
					ref={titleRef}
					id={titleId}
					className="settings-field-control h-control-form rounded-md!"
					value={summary}
					onChange={(event) => {
						setSummary(event.target.value);
						clearStatus();
					}}
					placeholder={t("report.titlePlaceholder")}
				/>
			</div>

			<div className="flex flex-col gap-1.5">
				<label className="settings-field-label" htmlFor={detailsId}>
					{t("report.whatHappened")}
				</label>
				<textarea
					id={detailsId}
					className="settings-field-control min-h-(--size-textarea-min) resize-none overflow-y-auto py-2.5 rounded-md!"
					value={details}
					onChange={(event) => {
						setDetails(event.target.value);
						clearStatus();
					}}
					placeholder={t("report.detailsPlaceholder")}
				/>
			</div>

			<div role="group" aria-label={t("report.destination")} className="flex flex-wrap items-center justify-end gap-3">
				{copyError ? (
					<p role="alert" className="text-caption leading-4 text-error">
						{copyError}
					</p>
				) : null}
				{copiedLabel && !copyError ? (
					<p className="text-caption leading-4 text-success">{t("report.draftCopied", { label: copiedLabel })}</p>
				) : null}
				<div className="flex items-center gap-1.5">
					{destinations.map((option) => {
						return (
							<Tooltip key={option.value}>
								<TooltipTrigger asChild>
									<Button
										type="button"
										variant="ghost"
										size="icon-sm"
										className="text-settings-muted hover:text-settings-label"
										disabled={!canSubmit}
										aria-label={option.action}
										onClick={() => {
											if (!canSubmit) return;
											void copyDraft(option.value);
										}}
									>
										<option.icon className="size-icon-sm" aria-hidden="true" />
									</Button>
								</TooltipTrigger>
								<TooltipContent>{option.action}</TooltipContent>
							</Tooltip>
						);
					})}
				</div>
			</div>
		</div>
	);
}
