import type { MessageKey } from "../../i18n/messages";
import { useTranslation } from "react-i18next";

export function BacklogEmptyState({ kind }: { kind: "none" | "filtered" | "archive" }) {
	const { t } = useTranslation();
	return <p className="px-3 py-8 text-sm text-muted-foreground">{t(`backlog.empty.${kind}` as MessageKey)}</p>;
}
