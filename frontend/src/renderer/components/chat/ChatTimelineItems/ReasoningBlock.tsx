import { PRODUCT_NAME } from "../../../../shared/brand";
import { Brain, Loader2 } from "lucide-react";
import { ChatMarkdown } from "../ChatMarkdown";
import { type ConversationActivity } from "../../../types/conversation";
import { Spinner } from "../../ui/spinner";

/**
 * The model's own account of what it is doing.
 *
 * Rendered as prose on a rail rather than as a collapsed row, because a reader only
 * ever sees this after deliberately turning reasoning on — hiding it again behind a
 * second click would make the toggle do nothing. It is markdown: the provider writes
 * bolded section headers into its summaries, and showing those as literal asterisks
 * was the visible half of this being unread.
 *
 * The text streams. Earlier builds read a field the payload does not have, so these
 * rows arrived blank even when the provider was sending; it now lands in `text`
 * while the model works and is replaced by the provider's settled summary when the
 * item completes, so one field is read either way.
 */
export function ReasoningBlock({ activity }: { activity: ConversationActivity }) {
	const text = activity.detail?.text ?? activity.detail?.reason ?? "";
	if (!text) return null;
	const streaming = activity.status === "running";

	return (
		<div className="flex gap-2.5 border-l-2 border-border-strong py-0.5 pl-3">
			<Brain aria-hidden="true" className="mt-[3px] size-3.5 shrink-0 text-muted-foreground/70" />
			<div className="min-w-0 flex-1">
				<div className="flex items-center gap-2">
					<span className="text-caption text-muted-foreground/70">
						{streaming ? "Thinking" : "Thought"}
					</span>
					{streaming ? (
						<Spinner icon={Loader2}
							aria-label="still thinking"
							
						 className="size-3 text-muted-foreground/50" />
					) : null}
				</div>
				<ChatMarkdown text={text} streaming={streaming} muted />
				{activity.detail?.textTruncated ? (
					<p className="mt-1 text-micro text-muted-foreground/70">
						This summary is longer than {PRODUCT_NAME} stores, so it stops early.
					</p>
				) : null}
			</div>
		</div>
	);
}

/* -------------------------------------------------------------------------- */
/* MCP tool calls                                                              */
/* -------------------------------------------------------------------------- */
