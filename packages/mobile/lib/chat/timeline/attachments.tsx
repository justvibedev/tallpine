import { Feather } from "../../icons";
import { createContext, useCallback, useContext, useState, type ReactNode } from "react";
import { Image, Platform, Pressable, Text, View } from "react-native";
import { authHeaders, httpBase } from "../../config";
import { haptics } from "../../haptics";
import { useApp } from "../../store";
import { useTheme, useThemedStyles } from "../../ThemeProvider";
import { MascotLamp } from "../../ui";
import { ChatMarkdown } from "../ChatMarkdown";
import { previewFilePath } from "../../api";
import { ImageViewer } from "../../ImageViewer";
import { attachmentName, attachmentTileSize, isImageAttachment, isSameAttachmentLoad, type AttachmentImageSource } from "../messageAttachments";
import type { ConversationItem } from "../types";
import { makeStyles } from "./styles";

export function SystemSignal({ icon, title, detail, danger }: { icon: keyof typeof Feather.glyphMap; title: string; detail?: unknown; danger?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return <View style={[styles.systemSignal, danger && { borderColor: t.red }]}><Feather name={icon} size={15} color={danger ? t.red : t.textTertiary} /><View style={{ flex: 1 }}><Text style={[styles.systemTitle, danger && { color: t.red }]}>{title}</Text>{detail ? <Text style={styles.systemDetail}>{String(detail)}</Text> : null}</View></View>;
}

/**
 * Files Tallpine staged into the worktree for a human message. Images load through the
 * daemon's preview-files route with the connection's Bearer header, the same
 * credential every other mobile request uses; anything else stays a name chip.
 * Images sit in the bubble as center-cropped tiles and open full-screen on tap.
 * RN 0.86 Android forwards HTTP headers only for array sources, on both surfaces.
 * Render it only for messages that carry attachments: it subscribes to the app
 * store, and doing that for every bubble would defeat TimelineItem's memo.
 */
export function StagedAttachments({ sessionId, paths, spaced }: { sessionId: string; paths: string[]; spaced: boolean }) {
	const { config } = useApp();
	const styles = useThemedStyles(makeStyles);
	const tileSize = attachmentTileSize(paths.filter(isImageAttachment).length);
	return <View style={[styles.attachments, spaced && styles.attachmentsSpaced]}>
		{paths.map((path) => {
			const source = config && isImageAttachment(path)
				? { uri: `${httpBase(config)}${previewFilePath(sessionId, path)}`, headers: authHeaders(config) }
				: undefined;
			return <StagedAttachment key={path} name={attachmentName(path)} source={source} tileSize={tileSize} />;
		})}
	</View>;
}

type OpenImage = (name: string, source: AttachmentImageSource) => void;
const ImageViewerHostContext = createContext<OpenImage | undefined>(undefined);

/**
 * Owns the one full-screen viewer for a timeline. User bubbles sit inside the iOS
 * native context menu (MenuView); an RN Modal mounted inside that SwiftUI host
 * presents invisibly, the bubble vanishes and the overlay swallows every touch.
 * Rendering the Modal here, outside the list rows, keeps it on a normal RN parent.
 */
export function ImageViewerHost({ children }: { children: ReactNode }) {
	const [viewing, setViewing] = useState<{ name: string; source: AttachmentImageSource }>();
	const open = useCallback<OpenImage>((name, source) => setViewing({ name, source }), []);
	const close = useCallback(() => setViewing(undefined), []);
	return <ImageViewerHostContext.Provider value={open}>
		{children}
		{viewing ? <ImageViewer visible name={viewing.name} source={viewing.source} onClose={close} /> : null}
	</ImageViewerHostContext.Provider>;
}

export function StagedAttachment({ name, source, tileSize }: { name: string; source?: AttachmentImageSource; tileSize: number }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	// The failure belongs to the load that failed, so a reconnect to another address
	// or a rotated password retries on its own; tapping the chip retries in place.
	const [failedLoad, setFailedLoad] = useState<AttachmentImageSource>();
	const [viewerOpen, setViewerOpen] = useState(false);
	const hostOpen = useContext(ImageViewerHostContext);
	if (!source) {
		return <View style={styles.attachmentChip}><Feather name="file-text" size={12} color={t.textTertiary} /><Text numberOfLines={1} style={styles.attachmentName}>{name}</Text></View>;
	}
	if (!isSameAttachmentLoad(failedLoad, source)) {
		// Cover-cropping a square reads as a deliberate thumbnail; the viewer shows the whole image.
		return <>
			<Pressable accessibilityRole="imagebutton" accessibilityLabel={`Open ${name}`} onPress={() => { haptics.tap(); if (hostOpen) hostOpen(name, source); else setViewerOpen(true); }} style={[styles.attachmentTile, { width: tileSize, height: tileSize }]}>
				<Image accessibilityIgnoresInvertColors source={Platform.OS === "android" ? [source] : source} resizeMode="cover" onError={() => setFailedLoad(source)} style={styles.attachmentTileImage} />
			</Pressable>
			{!hostOpen && viewerOpen ? <ImageViewer visible name={name} source={source} onClose={() => setViewerOpen(false)} /> : null}
		</>;
	}
	return <Pressable accessibilityRole="button" accessibilityLabel={`Retry loading ${name}`} hitSlop={6} onPress={() => { haptics.tap(); setFailedLoad(undefined); }} style={styles.attachmentChip}>
		<Feather name="refresh-cw" size={12} color={t.textTertiary} />
		<Text numberOfLines={1} style={styles.attachmentName}>{name}</Text>
		<Text style={styles.attachmentRetry}>Tap to retry</Text>
	</Pressable>;
}

export function OriginMessage({ message }: { message: Extract<ConversationItem, { kind: "message" }> }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const long = message.text.length > 600;
	const [expanded, setExpanded] = useState(false);
	return <View style={styles.originMessage}>
		<View style={styles.originHeader}><Feather name="radio" size={12} color={t.textTertiary} /><Text style={styles.originLabel}>{message.senderLabel || (message.origin === "automation" ? "Automation" : "Orchestrator")}</Text></View>
		{long && expanded ? <ChatMarkdown text={message.text} /> : <Text selectable numberOfLines={long ? 5 : undefined} style={styles.originText}>{message.text}</Text>}
		{long ? <Pressable accessibilityRole="button" accessibilityState={{ expanded }} onPress={() => { haptics.tap(); setExpanded((value) => !value); }} style={styles.originMore}><Feather name={expanded ? "chevron-up" : "chevron-right"} size={12} color={t.accent} /><Text style={styles.originMoreText}>{expanded ? "Hide report" : "Show full report"}</Text></Pressable> : null}
	</View>;
}

export function EmptyConversation({ controller }: { controller: string }) {
	const styles = useThemedStyles(makeStyles);
	return <View style={styles.empty}><View style={styles.emptyMascot}><MascotLamp size={48} /></View><Text style={styles.emptyTitle}>{controller === "connecting" ? "Connecting to the agent…" : "Start with a task"}</Text></View>;
}
