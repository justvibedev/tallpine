import { ChevronUp } from "lucide-react";
import { useTranslation } from "react-i18next";

/** One-line banner at the top of the timeline echoing the latest prompt; click jumps back to it. */
export function PinnedPrompt({ text, onJump }: { text: string; onJump: () => void }) {
	const { t } = useTranslation();
	return (
		<button
			type="button"
			data-testid="pinned-prompt"
			onClick={onJump}
			title={t("chat.timeline.pinnedPromptJump")}
			aria-label={t("chat.timeline.pinnedPromptAria", { text })}
			className="absolute inset-x-0 top-0 z-10 flex w-full items-center gap-2 border-b border-border bg-raised/95 px-4 py-1.5 text-left text-xs text-muted-foreground backdrop-blur hover:bg-surface hover:text-foreground"
		>
			<ChevronUp aria-hidden="true" className="size-3.5 shrink-0" />
			<span className="mx-auto flex w-full min-w-0 max-w-3xl"><span className="truncate">{text}</span></span>
		</button>
	);
}
