import { useState } from "react";
import { Check, ExternalLink, Loader2 } from "lucide-react";
import { DAEMON_TCP_OFF_ERROR } from "../../../shared/daemon-url";
import { appI18n } from "../../i18n";
import { tallpineBridge } from "../../lib/bridge";
import type { ConversationActivity } from "../../types/conversation";
import { ACCENT_ACTION_PILL, QUIET_ACTION_PILL } from "./action-pill";
import { Spinner } from "../ui/spinner";


import { FormRequest } from "./elicitation/ElicitationFormRequest";
import { DockFooter, DockHeader } from "./elicitation/ElicitationDockParts";
import { claudeQuestionGroups, enumOptions, propertyLabel, questionPrompt, safeExternalURL, type InputAction, type PropertyEntry } from "./elicitation/elicitation-model";
/**
 * Pending questions dock above the composer. After resolution, their prompt and
 * submitted answers remain readable in the transcript through ElicitationHistory.
 */
export function ElicitationDock({
	activity,
	onResolve,
}: {
	activity: ConversationActivity;
	onResolve?: (
		requestId: string,
		action: InputAction,
		content?: Record<string, unknown>,
	) => Promise<unknown> | void;
}) {
	const requestId = activity.requestId;
	const unavailable = !requestId || !onResolve;
	const [submitting, setSubmitting] = useState(false);
	const [error, setError] = useState<string>();

	async function resolve(action: InputAction, content?: Record<string, unknown>) {
		if (!requestId || !onResolve || submitting) return;
		setSubmitting(true);
		setError(undefined);
		try {
			await onResolve(requestId, action, content);
		} catch (reason) {
			setError(reason instanceof Error ? reason.message : "The answer could not be sent.");
		} finally {
			setSubmitting(false);
		}
	}

	return (
		<div
			role="group"
			aria-label="Agent question"
			data-testid="elicitation-dock"
			className="elicitation-dock animate-in fade-in-0 slide-in-from-bottom-1 duration-200 overflow-hidden rounded-[var(--radius-chat-composer)] border border-border-strong bg-surface shadow-sm"
		>
			{activity.detail?.inputMode === "url" ? (
				<URLRequest activity={activity} disabled={submitting || unavailable} onResolve={resolve} />
			) : (
				<FormRequest activity={activity} disabled={submitting || unavailable} onResolve={resolve} />
			)}

			{error ? (
				<p role="alert" className="px-3 pb-2 text-xs leading-snug text-destructive">
					{error}
				</p>
			) : null}
		</div>
	);
}

export function ElicitationHistory({ activity }: { activity: ConversationActivity }) {
	const response = activity.inputResponse;
	const answered = (activity.status === "resolved" || activity.status === "completed") && response?.action === "accept";
	const outcome = activity.status === "failed" ? "Question expired"
		: activity.status === "cancelled" || response?.action === "cancel" ? "Cancelled"
			: response?.action === "decline" ? "Skipped"
				: answered ? "Answered" : "Question closed";
	const schema = activity.detail?.schema;
	const properties = Object.entries(schema?.properties ?? {});
	const groups = claudeQuestionGroups(properties);
	const message = activity.detail?.message ?? schema?.title ?? activity.summary;
	const content = answered ? response.content : undefined;

	return (
		<section aria-label="Agent question history" className="min-w-0 space-y-3 rounded-lg border border-border bg-surface/45 p-3 text-xs leading-relaxed">
			<p className="font-medium text-muted-foreground">{outcome}</p>
			{groups ? groups.map((group) => {
				const question = group[0];
				if (!question) return null;
				return (
					<div key={question[0]} className="space-y-1">
						<p className="whitespace-pre-wrap break-words font-medium text-foreground">
							{questionPrompt(question, groups.length === 1 ? activity.detail?.message : undefined)}
						</p>
						{answered ? <HistoryAnswers properties={group} content={content} /> : null}
					</div>
				);
			}) : (
				<div className="space-y-1">
					<p className="whitespace-pre-wrap break-words font-medium text-foreground">{message}</p>
					{schema?.description ? <p className="whitespace-pre-wrap break-words">{schema.description}</p> : null}
					{answered && properties.length > 0 ? <HistoryAnswers properties={properties} content={content} /> : null}
				</div>
			)}
			{activity.detail?.inputMode === "url" && activity.detail.url ? (
				<p className="break-all text-muted-foreground">{activity.detail.url}</p>
			) : null}
		</section>
	);
}

function HistoryAnswers({ properties, content }: { properties: PropertyEntry[]; content?: Record<string, unknown> }) {
	const answers = properties.filter(([name]) => {
		const value = content?.[name];
		return value !== undefined && value !== "" && !(Array.isArray(value) && value.length === 0);
	});
	if (answers.length === 0) {
		return <p className="text-muted-foreground">{content ? "No answer provided" : "Answer unavailable"}</p>;
	}
	return (
		<dl className="space-y-1">
			{answers.map(([name, property]) => {
				const value = content?.[name];
				const options = enumOptions(property);
				function label(value: unknown): string {
					if (typeof value === "boolean") return value ? "Yes" : "No";
					if (typeof value === "number") return String(value);
					if (typeof value !== "string") return "Answer unavailable";
					const option = options.find((option) => option.value === value);
					return option ? [option.label, option.description].filter(Boolean).join(" — ") : value;
				}
				return (
					<div key={name} className="min-w-0">
						<dt className="text-muted-foreground">{propertyLabel([name, property])}</dt>
						<dd className="mt-0.5 flex min-w-0 items-start gap-2 text-foreground">
							{/* The same tile the dock marks a picked choice with. */}
							<span aria-hidden="true" className="mt-0.5 grid size-4 shrink-0 place-items-center rounded bg-logo-accent text-logo-accent-foreground">
								<Check className="size-3" />
							</span>
							<span className="min-w-0 whitespace-pre-wrap break-words">
								{Array.isArray(value) ? value.map(label).join(", ") : label(value)}
							</span>
						</dd>
					</div>
				);
			})}
		</dl>
	);
}

/** The full question stays visible above its answers, alongside the step count. */
function URLRequest({
	activity,
	disabled,
	onResolve,
}: {
	activity: ConversationActivity;
	disabled: boolean;
	onResolve: (action: InputAction, content?: Record<string, unknown>) => Promise<void>;
}) {
	const rawURL = activity.detail?.url ?? "";
	const parsed = safeExternalURL(rawURL);
	const [opening, setOpening] = useState(false);
	const [openError, setOpenError] = useState<string>();

	async function consent() {
		if (!parsed || opening) return;
		setOpening(true);
		setOpenError(undefined);
		try {
			// Opening is the consented action. Tell the provider only after Electron
			// accepted it, so an OS-level refusal is never reported as success.
			await tallpineBridge.app.openExternal(parsed.href);
			await onResolve("accept");
		} catch (error) {
			setOpenError(
				String(error).includes(DAEMON_TCP_OFF_ERROR)
					? appI18n.t("browser.externalNeedsLoopbackTcp")
					: "The link could not be opened. Nothing was approved.",
			);
		} finally {
			setOpening(false);
		}
	}

	return (
		<>
			<DockHeader title={activity.detail?.message || activity.summary} />
			{parsed ? (
				<div className="flex min-h-10 min-w-0 items-center gap-2.5 px-3 py-2">
					<div className="min-w-0 flex-1">
						<p className="truncate text-xs leading-relaxed text-foreground">{parsed.hostname}</p>
						<p className="mt-0.5 break-all font-mono text-caption leading-relaxed text-muted-foreground">
							{parsed.href}
						</p>
					</div>
				</div>
			) : (
				<p role="alert" className="px-3 py-2 text-xs leading-snug text-destructive">
					The provider supplied an unsafe or invalid URL. It was not opened.
				</p>
			)}
			{openError ? (
				<p role="alert" className="px-3 pb-1 text-xs leading-snug text-destructive">
					{openError}
				</p>
			) : null}
			<DockFooter>
				<div className="flex items-center gap-1.5">
					<button type="button" className={QUIET_ACTION_PILL} disabled={disabled} onClick={() => onResolve("cancel")}>
						Cancel
					</button>
					<button type="button" className={QUIET_ACTION_PILL} disabled={disabled} onClick={() => onResolve("decline")}>
						Decline
					</button>
				</div>
				<button
					type="button"
					className={ACCENT_ACTION_PILL}
					disabled={disabled || !parsed}
					onClick={() => void consent()}
				>
					{opening ? (
						<Spinner icon={Loader2} className="size-3.5" />
					) : (
						<ExternalLink aria-hidden="true" className="size-3.5" />
					)}
					Open {parsed?.hostname ?? "link"}
				</button>
			</DockFooter>
		</>
	);
}

