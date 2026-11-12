/**
 * Inline players for local video and audio files an agent names in its reply.
 *
 * Like the image strip, the reply keeps its path text and gains the player under
 * it. The file loads from the same confined, streamed route the Files panel
 * uses; one that does not load (missing, unreadable, outside every served
 * directory) disappears.
 */

import { memo, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { fileMediaKind } from "../../lib/file-media";
import { findLocalMediaPaths } from "../../../shared/local-image-paths";
import { MediaElement } from "../FileMediaPreview";
import { useChatLocalImageUrl } from "./chat-image-source";

function ChatLocalMediaPlayer({ path }: { path: string }) {
	const { t } = useTranslation();
	const url = useChatLocalImageUrl(path, true);
	const [failed, setFailed] = useState(false);
	const kind = fileMediaKind(path);
	if (!url || failed || (kind !== "video" && kind !== "audio")) return null;
	const name = path.slice(path.lastIndexOf("/") + 1);
	return (
		<MediaElement
			kind={kind}
			src={url}
			label={t("chat.media.player", { name })}
			onError={() => setFailed(true)}
			className={kind === "video" ? "block max-h-[60vh] w-full max-w-[760px] rounded-md border border-border bg-black" : "block w-full max-w-lg"}
		/>
	);
}

/** One player per local video or audio file `text` names; nothing when it names none. */
export const ChatLocalMedia = memo(function ChatLocalMedia({ text }: { text: string }) {
	const { t } = useTranslation();
	const paths = useMemo(() => findLocalMediaPaths(text), [text]);
	if (paths.length === 0) return null;
	return (
		<div role="group" aria-label={t("chat.media.group")} data-chat-local-media="" className="mt-2 flex flex-col gap-2 empty:hidden">
			{paths.map((path) => <ChatLocalMediaPlayer key={path} path={path} />)}
		</div>
	);
});
