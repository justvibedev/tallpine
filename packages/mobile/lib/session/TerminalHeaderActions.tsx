import { ActivityIndicator, Pressable, View } from "react-native";
import { Feather } from "../icons";
import { iconSize, press } from "../tokens";
import type { Theme } from "../theme";
import type { makeStyles } from "./terminalStyles";

type Props = {
	t: Theme;
	styles: ReturnType<typeof makeStyles>;
	rechecking: boolean;
	starting: boolean;
	transitionActive: boolean;
	interfaceSupported: boolean | undefined;
	hasPreview: boolean;
	onInterface: () => void;
	onPreview: () => void;
	onFiles: () => void;
	hasArtifacts?: boolean;
	onArtifacts?: () => void;
};

/** Right-hand nav actions: Chat interface switch, preview and session files. */
export function TerminalHeaderActions({ t, styles, rechecking, starting, transitionActive, interfaceSupported, hasPreview, onInterface, onPreview, onFiles, hasArtifacts = false, onArtifacts }: Props) {
	return (
	<View style={styles.headerActions}>
		<Pressable
			hitSlop={10}
			accessibilityLabel="Open Chat interface"
			accessibilityState={{
				busy: transitionActive || starting || rechecking,
			}}
			disabled={rechecking || starting || transitionActive}
			onPress={onInterface}
			style={({ pressed }) => [styles.headerBrowserBtn, pressed && { opacity: press.opacity }]}
		>
			{/* A spinner rather than a dimmed glyph: the recheck only runs while
			    the icon is already faint, so fading it further reads as broken.
			    `starting` shows it too — that POST is disabled-but-silent for up
			    to REQUEST_TIMEOUT_MS otherwise, the same dead-button shape the
			    recheck spinner exists to remove. */}
			{rechecking || starting ? (
				<ActivityIndicator size="small" color={t.accent} />
			) : (
				<Feather
					name={transitionActive ? "repeat" : "message-square"}
					size={iconSize.md}
					color={interfaceSupported ? t.accent : t.textFaint}
				/>
			)}
		</Pressable>
		<Pressable
			hitSlop={12}
			accessibilityLabel="Open preview"
			onPress={onPreview}
			style={({ pressed }) => [styles.headerBrowserBtn, pressed && { opacity: press.opacity }]}
		>
			<Feather
				name="globe"
				size={iconSize.lg}
				color={hasPreview ? t.green : t.textSecondary}
			/>
			{hasPreview && <View style={styles.browserReadyDot} />}
		</Pressable>
		<Pressable
			hitSlop={10}
			accessibilityRole="button"
			accessibilityLabel="Session files and changes"
			onPress={onFiles}
			style={({ pressed }) => [styles.headerBrowserBtn, pressed && { opacity: press.opacity }]}
		>
			<Feather name="file-diff" size={iconSize.lg} color={t.textSecondary} />
		</Pressable>
		{hasArtifacts ? (
			<Pressable
				hitSlop={10}
				accessibilityRole="button"
				accessibilityLabel={"Artifacts"}
				onPress={onArtifacts}
				style={({ pressed }) => [styles.headerBrowserBtn, pressed && { opacity: press.opacity }]}
			>
				<Feather name="layers" size={iconSize.md} color={t.textSecondary} />
			</Pressable>
		) : null}
	</View>
	);
}
