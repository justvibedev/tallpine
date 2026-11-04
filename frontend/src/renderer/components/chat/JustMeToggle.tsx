import { UserRound } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { useChatJustMeStore } from "../../stores/chat-just-me-store";
import { TopbarButton } from "../TopbarButton";

/** Header switch that folds orchestration traffic out of the chat. */
export function JustMeToggle() {
	const { t } = useTranslation();
	const justMe = useChatJustMeStore((state) => state.justMe);
	const toggle = useChatJustMeStore((state) => state.toggleJustMe);
	return (
		<TopbarButton
			variant="icon"
			aria-pressed={justMe}
			aria-label={t("chat.justMe.toggle")}
			title={t("chat.justMe.toggleHint")}
			onClick={toggle}
			className={cn(justMe && "bg-interactive-hover text-foreground")}
		>
			<UserRound aria-hidden="true" className="size-icon-base" />
		</TopbarButton>
	);
}
