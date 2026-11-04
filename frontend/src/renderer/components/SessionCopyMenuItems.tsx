import { Copy, Link } from "lucide-react";
import { useTranslation } from "react-i18next";
import { copyLinkText } from "../lib/copy-link";
import { sessionLink } from "../lib/session-links";
import { ContextMenuItem } from "./ui/context-menu";
import { DropdownMenuItem } from "./ui/dropdown-menu";

type Props = { projectId: string; sessionId: string };

/** "Copy session ID" and "Copy link" entries for a session's ... menu (dropdown variant). */
export function SessionCopyDropdownItems({ projectId, sessionId }: Props) {
	const { t } = useTranslation();
	return (
		<>
			<DropdownMenuItem onSelect={() => copyLinkText(sessionId)}>
				<Copy aria-hidden="true" className="size-icon-lg" />
				{t("session.copyId")}
			</DropdownMenuItem>
			<DropdownMenuItem onSelect={() => copyLinkText(sessionLink(projectId, sessionId))}>
				<Link aria-hidden="true" className="size-icon-lg" />
				{t("session.copyLink")}
			</DropdownMenuItem>
		</>
	);
}

/** Same entries for a right-click context menu. */
export function SessionCopyContextItems({ projectId, sessionId }: Props) {
	const { t } = useTranslation();
	return (
		<>
			<ContextMenuItem onSelect={() => copyLinkText(sessionId)}>
				<Copy aria-hidden="true" />
				{t("session.copyId")}
			</ContextMenuItem>
			<ContextMenuItem onSelect={() => copyLinkText(sessionLink(projectId, sessionId))}>
				<Link aria-hidden="true" />
				{t("session.copyLink")}
			</ContextMenuItem>
		</>
	);
}
