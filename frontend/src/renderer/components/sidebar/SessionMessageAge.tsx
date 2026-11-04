import { useTranslation } from "react-i18next";
import { memo } from "react";
import { type WorkspaceSession } from "../../types/workspace";
import { formatTimeCompact, formatTimeTerse } from "../../lib/format-time";

export const SessionMessageAge = memo(function SessionMessageAge({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	const at = session.lastInteractionAt ?? session.lastUserMessageAt;
	if (!at) return null;

	return (
		<time
			className="absolute inset-y-0 right-2 z-[1] flex min-w-0 shrink-0 items-center whitespace-nowrap font-sans text-micro tabular-nums text-passive opacity-100 group-has-[:focus-visible]/session-row:opacity-0"
			data-session-message-age=""
			dateTime={at}
			title={t("shell.lastMessageAt", { time: formatTimeCompact(at) })}
		>
			{formatTimeTerse(at)}
		</time>
	);
});
