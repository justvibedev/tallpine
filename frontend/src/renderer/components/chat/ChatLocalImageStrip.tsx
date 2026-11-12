/**
 * Thumbnails for local image paths an agent names in its reply.
 *
 * Agents often list a screenshot's path instead of rendering it, and the path
 * alone shows nothing. The reply keeps the path text (clickable, with its
 * right-click menu) and gains a row of thumbnails under it. Each thumbnail
 * loads lazily from a route that confines the path itself; one that does not
 * load (missing, unreadable, outside every served directory) disappears.
 */

import { memo, useMemo, useState, type MouseEvent } from "react";
import { useTranslation } from "react-i18next";
import { mediaCrossOrigin } from "../../lib/file-media";
import { cn } from "../../lib/utils";
import { findLocalImagePaths } from "../../../shared/local-image-paths";
import { LocalPathMenuItems, useLocalPathMenu } from "../LocalPathMenuItems";
import { ContextMenu, ContextMenuContent, ContextMenuTrigger } from "../ui/context-menu";
import { ChatImageViewer } from "./ChatImageLightbox";
import { CHAT_IMAGE_LABEL_ATTRIBUTE, CHAT_IMAGE_SCOPE_ATTRIBUTE, CHAT_IMAGE_URL_ATTRIBUTE, collectChatImageSet, type ChatImageItem } from "./chat-image-set";
import { useChatLocalImageUrl } from "./chat-image-source";

function fileName(path: string): string {
	return path.slice(path.lastIndexOf("/") + 1);
}

function ChatLocalImage({ path }: { path: string }) {
	const { t } = useTranslation();
	const url = useChatLocalImageUrl(path);
	const menu = useLocalPathMenu(path);
	const [state, setState] = useState<"loading" | "loaded" | "failed">("loading");
	const [viewing, setViewing] = useState<{ items: ChatImageItem[]; index: number } | null>(null);
	if (!url || state === "failed") return null;
	const name = fileName(path);
	return (
		<>
			<ContextMenu onOpenChange={menu.onOpenChange}>
				<ContextMenuTrigger asChild>
					<button
						type="button"
						title={path}
						onClick={(event: MouseEvent<HTMLButtonElement>) => setViewing(collectChatImageSet(event.currentTarget, { url, src: url, label: name }))}
						{...{ [CHAT_IMAGE_URL_ATTRIBUTE]: url, [CHAT_IMAGE_LABEL_ATTRIBUTE]: name }}
						aria-label={t("chat.image.open", { name })}
						// Hidden, not removed, until it decodes: a lazy image needs a laid-out box to start loading.
						className={cn(
							"block h-28 w-full cursor-zoom-in overflow-hidden rounded-md border border-border bg-surface transition-opacity duration-150 hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring motion-reduce:transition-none",
							state !== "loaded" && "invisible",
						)}
					>
						<img
							crossOrigin={mediaCrossOrigin(url)}
							src={url}
							alt={name}
							loading="lazy"
							onLoad={() => setState("loaded")}
							onError={() => setState("failed")}
							className="block size-full object-contain"
						/>
					</button>
				</ContextMenuTrigger>
				<ContextMenuContent className="min-w-52">
					<LocalPathMenuItems menu={menu} />
				</ContextMenuContent>
			</ContextMenu>
			{state === "loaded" ? <ChatImageViewer items={viewing?.items ?? []} index={viewing?.index} open={viewing !== null} onOpenChange={(next) => { if (!next) setViewing(null); }} /> : null}
		</>
	);
}

/** Up to three thumbnails per line for `paths`; one thumbnail's viewer pages through the rest. */
export function ChatLocalImageGrid({ paths, className, scope = true }: { paths: string[]; className?: string; /** Own the paging set; false when an enclosing message already does. */ scope?: boolean }) {
	const { t } = useTranslation();
	if (paths.length === 0) return null;
	return (
		<div
			role="group"
			aria-label={t("chat.image.gallery")}
			data-chat-local-images=""
			{...(scope ? { [CHAT_IMAGE_SCOPE_ATTRIBUTE]: "" } : {})}
			className={cn("grid max-w-xl grid-cols-3 gap-2 empty:hidden", className)}
		>
			{paths.map((path) => <ChatLocalImage key={path} path={path} />)}
		</div>
	);
}

/** The thumbnails for the local images `text` names; nothing when it names none. */
export const ChatLocalImageStrip = memo(function ChatLocalImageStrip({ text }: { text: string }) {
	const paths = useMemo(() => findLocalImagePaths(text), [text]);
	return <ChatLocalImageGrid paths={paths} className="mt-2" scope={false} />;
});
