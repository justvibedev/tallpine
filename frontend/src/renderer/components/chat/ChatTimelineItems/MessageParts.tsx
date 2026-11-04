import { attachmentName, attachmentURL, IMAGE_ATTACHMENT_PATH, splitInlineImagePaths } from "@tallpine/shared/chat/attachments";
import { ChatImage } from "../ChatImage";
import { Fragment, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { File as FileIcon } from "lucide-react";
import { cn } from "../../../lib/utils";
import { type DeliveryState } from "../../../types/conversation";
import { formatMessageTimestamp } from "./format";

/**
 * Prose whose inline staged image paths (written by the composer's image chips)
 * render as chips that open the image. "Image N" counts images in attachment
 * order, the same label the attachment row and the composer use.
 */
export function ProseWithInlineImages({
	text,
	attachments,
	sessionId,
	apiBaseUrl,
	renderText,
}: {
	text: string;
	attachments: string[];
	sessionId: string;
	apiBaseUrl: string | null;
	renderText: (text: string) => ReactNode;
}) {
	const { t } = useTranslation();
	const images = attachments.filter((path) => IMAGE_ATTACHMENT_PATH.test(path));
	if (apiBaseUrl === null || images.length === 0) return renderText(text);
	return splitInlineImagePaths(text, (path) => images.includes(path)).map((segment, index) =>
		segment.path === undefined ? (
			<Fragment key={index}>{renderText(segment.text)}</Fragment>
		) : (
			<ChatImage
				key={index}
				inline
				src={attachmentURL(apiBaseUrl, sessionId, segment.path)}
				alt={t("chat.image.numbered", { index: images.indexOf(segment.path) + 1 })}
			/>
		),
	);
}

export function StagedAttachmentItems({
	paths,
	sessionId,
	apiBaseUrl,
	ariaLabel,
	className,
}: {
	paths: string[];
	sessionId: string;
	apiBaseUrl: string | null;
	ariaLabel: string;
	className?: string;
}) {
	const { t } = useTranslation();
	if (paths.length === 0) return null;
	const images = paths.filter((path) => IMAGE_ATTACHMENT_PATH.test(path));
	return (
		<ul aria-label={ariaLabel} className={cn("flex max-w-full flex-wrap gap-2", className)}>
			{paths.map((path) => {
				const name = attachmentName(path);
				return IMAGE_ATTACHMENT_PATH.test(path) && apiBaseUrl !== null ? (
					<li key={path} className="max-w-full">
						<ChatImage
							src={attachmentURL(apiBaseUrl, sessionId, path)}
							alt={t("chat.image.numbered", { index: images.indexOf(path) + 1 })}
						/>
					</li>
				) : (
					<li
						key={path}
						title={path}
						className="flex max-w-full items-center gap-1.5 rounded-md border border-border bg-background px-2 py-1.5 text-xs text-muted-foreground"
					>
						<FileIcon aria-hidden="true" className="size-3.5 shrink-0" />
						<span className="truncate">{name}</span>
					</li>
				);
			})}
		</ul>
	);
}

export function MessageTimestamp({ createdAt }: { createdAt: string }) {
	return (
		<span className="shrink-0 px-0.5 text-caption tabular-nums text-muted-foreground/75" aria-label={`Sent ${formatMessageTimestamp(createdAt)}`}>
			{formatMessageTimestamp(createdAt)}
		</span>
	);
}

export function QueuedNote() {
	return (
		<div className="flex items-center gap-2 text-xs text-muted-foreground">
			<span>Queued · sends when the agent finishes</span>
		</div>
	);
}

/**
 * Delivery state, stated rather than implied. `uncertain` is its own outcome:
 * the provider may have accepted the turn while Tallpine lost the connection, and
 * pretending otherwise in either direction would be a lie.
 */
export function DeliveryNote({ state }: { state: DeliveryState }) {
	const copy: Record<DeliveryState, string> = {
		queued: "Queued — will send when the agent is idle",
		sending: "Sending…",
		accepted: "Delivered",
		uncertain: "Delivery uncertain — not retried automatically",
		failed: "Not delivered",
	};
	return (
		<span
			className={cn(
				"text-caption leading-none",
				state === "uncertain" || state === "failed" ? "text-warning" : "text-muted-foreground",
			)}
		>
			{copy[state]}
		</span>
	);
}

/* -------------------------------------------------------------------------- */
/* activities                                                                  */
/* -------------------------------------------------------------------------- */
