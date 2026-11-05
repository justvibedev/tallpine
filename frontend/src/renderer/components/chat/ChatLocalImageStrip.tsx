/**
 * Thumbnails for local image paths an agent names in its reply.
 *
 * Agents often list a screenshot's path instead of rendering it, and the path
 * alone shows nothing. The reply keeps the path text (clickable, with its
 * right-click menu) and gains a row of thumbnails under it. Each thumbnail
 * loads lazily from a route that confines the path itself; one that does not
 * load (missing, unreadable, outside every served directory) disappears.
 */

import { memo, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { mediaCrossOrigin } from "../../lib/file-media";
import { cn } from "../../lib/utils";
import { findLocalImagePaths } from "../../../shared/local-image-paths";
import { LocalPathMenuItems, useLocalPathMenu } from "../LocalPathMenuItems";
import { ContextMenu, ContextMenuContent, ContextMenuTrigger } from "../ui/context-menu";
import { ChatImageViewer } from "./ChatImage";
import { useChatLocalImageUrl } from "./chat-image-source";

function fileName(path: string): string {
	return path.slice(path.lastIndexOf("/") + 1);
}

function ChatLocalImage({ path }: { path: string }) {
	const { t } = useTranslation();
	const url = useChatLocalImageUrl(path);
	const menu = useLocalPathMenu(path);
	const [state, setState] = useState<"loading" | "loaded" | "failed">("loading");
	const [open, setOpen] = useState(false);
	if (!url || state === "failed") return null;
	const name = fileName(path);
	return (
		<>
			<ContextMenu onOpenChange={menu.onOpenChange}>
				<ContextMenuTrigger asChild>
					<button
						type="button"
						title={path}
						onClick={() => setOpen(true)}
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
			{state === "loaded" ? <ChatImageViewer url={url} label={name} open={open} onOpenChange={setOpen} /> : null}
		</>
	);
}

/** A row of up to three thumbnails per line for the local images `text` names; nothing when it names none. */
export const ChatLocalImageStrip = memo(function ChatLocalImageStrip({ text }: { text: string }) {
	const { t } = useTranslation();
	const paths = useMemo(() => findLocalImagePaths(text), [text]);
	if (paths.length === 0) return null;
	return (
		<div role="group" aria-label={t("chat.image.gallery")} data-chat-local-images="" className="mt-2 grid max-w-xl grid-cols-3 gap-2 empty:hidden">
			{paths.map((path) => <ChatLocalImage key={path} path={path} />)}
		</div>
	);
});
