import { useLocalSearchParams, useRouter } from "expo-router";
import { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, FlatList, Pressable, RefreshControl, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { getSessionArtifacts, type SessionArtifact } from "../lib/api";
import { artifactIcon, artifactSizeLabel, sortArtifacts } from "../lib/artifactsView";
import { backOr } from "../lib/backNavigation";
import { haptics } from "../lib/haptics";
import { Feather } from "../lib/icons";
import { useApp } from "../lib/store";
import type { Theme } from "../lib/theme";
import { useTheme, useThemedStyles } from "../lib/ThemeProvider";
import { iconSize, space, type } from "../lib/tokens";
import { Button, EmptyState, HeaderIconButton, ScreenHeader } from "../lib/ui";

export { RouteErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";

/** Every file in one session's artifact folder; a row opens the existing artifact viewer. */
export default function ArtifactsScreen() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const insets = useSafeAreaInsets();
	const { sessionId, hostId } = useLocalSearchParams<{ sessionId: string; hostId?: string }>();
	const { configForHost, config } = useApp();
	const cfg = hostId ? configForHost(hostId) : config;
	const [files, setFiles] = useState<SessionArtifact[]>();
	const [failed, setFailed] = useState(false);
	const [refreshing, setRefreshing] = useState(false);

	const load = useCallback(async () => {
		if (!cfg || !sessionId) {
			setFailed(true);
			return;
		}
		try {
			setFiles(sortArtifacts(await getSessionArtifacts(cfg, sessionId)));
			setFailed(false);
		} catch {
			setFailed(true);
		}
	}, [cfg, sessionId]);
	useEffect(() => { void load(); }, [load]);

	return (
		<View style={styles.screen}>
			<View style={{ height: insets.top }} />
			<ScreenHeader title="Artifacts" left={<HeaderIconButton icon="back" label="Back" onPress={() => backOr(router, "/")} />} />
			{!files && !failed ? (
				<View style={styles.center}><ActivityIndicator color={t.accent} /></View>
			) : failed && !files ? (
				<EmptyState icon="wifi-off" title="Couldn't load artifacts" message="Check the computer is reachable, then try again." action={<Button title="Retry" icon="refresh-cw" onPress={() => void load()} />} />
			) : (
				<FlatList
					data={files}
					keyExtractor={(file) => file.path}
					contentContainerStyle={{ paddingBottom: insets.bottom + 40 }}
					refreshControl={<RefreshControl refreshing={refreshing} tintColor={t.accent} onRefresh={async () => { setRefreshing(true); await load(); setRefreshing(false); }} />}
					ListEmptyComponent={<EmptyState icon="file" title="No artifacts yet" message="Files the worker writes to its artifact folder appear here." />}
					renderItem={({ item }) => (
						<Pressable
							accessibilityRole="button"
							accessibilityLabel={`${item.name}, ${artifactSizeLabel(item.size)}`}
							onPress={() => {
								haptics.tap();
								router.push({ pathname: "/artifact/[sessionId]", params: { sessionId, entry: item.path, hostId } });
							}}
							style={({ pressed }) => [styles.row, pressed && { backgroundColor: t.bgSubtle }]}
						>
							<Feather name={artifactIcon(item.kind)} size={iconSize.md} color={t.textSecondary} />
							<View style={{ flex: 1 }}>
								<Text style={styles.title} numberOfLines={1}>{item.name}</Text>
								<Text style={styles.meta} numberOfLines={1}>{item.path !== item.name ? `${item.path} · ` : ""}{artifactSizeLabel(item.size)}</Text>
							</View>
						</Pressable>
					)}
				/>
			)}
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		screen: { flex: 1, backgroundColor: t.bgBase },
		center: { flex: 1, alignItems: "center", justifyContent: "center", paddingVertical: 60 },
		row: { flexDirection: "row", alignItems: "center", gap: space.sm, paddingHorizontal: space.lg, paddingVertical: space.sm, minHeight: 52, borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: t.borderSubtle },
		title: { color: t.textPrimary, fontSize: type.callout.fontSize, lineHeight: type.callout.lineHeight },
		meta: { color: t.textTertiary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight },
	});
