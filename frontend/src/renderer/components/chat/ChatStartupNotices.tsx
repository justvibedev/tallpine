import { type ComponentProps } from "react";
import { TriangleAlert, Undo2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { SessionStartup } from "./SessionStartup";


export function OrchestratorStartupStatus({ error, onRetry, retrying, retryError }: ComponentProps<typeof SessionStartup>) {
	const { t } = useTranslation();
	return (
		<div data-testid="orchestrator-startup-status" className="px-2 text-xs text-muted-foreground">
			<div role="alert" className="flex items-center gap-2">
				<TriangleAlert aria-hidden="true" className="size-3.5 text-destructive" />
				<span>{t("chat.startup.failed")}</span>
				{onRetry ? <button type="button" className="ml-auto text-foreground underline underline-offset-2" onClick={onRetry} disabled={retrying}>{retrying ? t("chat.startup.retrying") : t("chat.startup.retry")}</button> : null}
			</div>
			{error ? <details className="mt-2"><summary className="cursor-pointer">{t("chat.errorDetails")}</summary><p className="mt-1 break-words">{error}</p></details> : null}
			{retryError ? <p role="alert" className="mt-1 text-destructive">{retryError}</p> : null}
		</div>
	);
}

/**
 * What an undo took away.
 *
 * Stated above the composer rather than as a timeline entry, because the discarded
 * turns keep their original sequence positions and this notice has none of its own —
 * placing it in the timeline would be claiming an order it cannot know. It sits where
 * the user is looking after an undo, and it says the part that matters: the agent
 * does not remember.
 */
export function RolledBackNotice({ count }: { count: number }) {
	return (
		<p className="flex items-center gap-1.5 text-xs text-muted-foreground">
			<Undo2 aria-hidden="true" className="size-3 shrink-0" />
			{count === 1
				? "1 turn was rolled back. The agent no longer remembers it."
				: `${count} turns were rolled back. The agent no longer remembers them.`}
		</p>
	);
}
