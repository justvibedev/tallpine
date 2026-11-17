import { useTranslation } from "react-i18next";
import type { LspInstallJob, LspInstallTarget, useLspInstall } from "../../lib/lsp-install";
import { Button } from "../ui/button";

type Install = ReturnType<typeof useLspInstall>;

/** Runs the daemon's fixed install for one language and target; disabled while any install of the project runs. */
export function LspInstallButton({ install, language, target, label }: { install: Install; language: string; target: LspInstallTarget; label: string }) {
	const { t } = useTranslation();
	const mine = install.job?.state === "running" && install.job.language === language && install.job.target === target;
	return <Button size="sm" variant="outline" type="button" disabled={install.running} aria-label={t("settings.instructions.lsp.installLabel", { label })}
		onClick={() => install.start({ language, target })}>
		{mine ? t("settings.instructions.lsp.installing") : t("settings.instructions.lsp.install")}
	</Button>;
}

function jobLine(job: LspInstallJob, t: (key: string, options?: Record<string, unknown>) => string): string {
	if (job.state === "running") return t("settings.instructions.lsp.installRunning", { command: job.command });
	if (job.state === "succeeded") return t("settings.instructions.lsp.installSucceeded");
	return t("settings.instructions.lsp.installFailed", { error: job.error ?? "" });
}

/** The last install of this language: progress, the reason it failed and the output tail. */
export function LspInstallStatus({ install, language }: { install: Install; language: string }) {
	const { t } = useTranslation();
	const { job, startError, startFor } = install;
	if (startError && startFor?.language === language) {
		return <p role="alert" className="text-xs leading-4 text-error">{t("settings.instructions.lsp.installFailed", { error: startError })}</p>;
	}
	if (!job || job.language !== language) return null;
	return <div data-testid={`lsp-install-${language}`} className="flex flex-col gap-1">
		<p role={job.state === "failed" ? "alert" : "status"} className={job.state === "failed" ? "text-xs leading-4 text-error" : "text-xs leading-4 text-settings-muted"}>{jobLine(job, t)}</p>
		{job.output && job.state !== "succeeded"
			? <pre aria-label={t("settings.instructions.lsp.installOutput")} className="max-h-40 overflow-auto whitespace-pre-wrap break-all rounded-sm bg-settings-input p-2 font-mono text-[11px] leading-4 text-settings-label">{job.output}</pre>
			: null}
	</div>;
}
