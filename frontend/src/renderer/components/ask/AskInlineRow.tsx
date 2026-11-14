import { MessageCircleQuestion } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * The mark an ask leaves on the turn that asked, like a rendered page does. The
 * "Waiting on you" strip's jump link scrolls here; the strip holds the live
 * state and the answer buttons.
 */
export function AskInlineRow({ ask, summary }: { ask: { id: string; ref: string }; summary: string }) {
	const { t } = useTranslation();
	return (
		<div data-ask-ref={ask.ref} data-testid="ask-inline" className="flex items-start gap-2 px-1 py-1 text-xs text-muted-foreground">
			<MessageCircleQuestion aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" strokeWidth={1.75} />
			<span className="min-w-0 flex-1"><span className="font-medium text-foreground">{t("ask.inline")}</span> · {summary}</span>
		</div>
	);
}
