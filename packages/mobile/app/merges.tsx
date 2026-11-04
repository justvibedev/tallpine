import { useLocalSearchParams, useRouter } from "expo-router";
import { useCallback, useEffect, useMemo, useState } from "react";
import { ActivityIndicator, RefreshControl, SectionList, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { getMergeDigest, type MergeDigest } from "../lib/api";
import { backOr } from "../lib/backNavigation";
import { openGitHub } from "../lib/openGitHub";
import { mergeDigestEmpty, mergeDigestLanes, overlapLine, type MergeDigestRow } from "../lib/mergeDigestView";
import { useApp } from "../lib/store";
import type { Theme } from "../lib/theme";
import { useTheme, useThemedStyles } from "../lib/ThemeProvider";
import { space, type } from "../lib/tokens";
import { Button, EmptyState, HeaderIconButton, ListSectionHeader, ScreenHeader } from "../lib/ui";

export { RouteErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";

/** The cross-lane merge digest for one project: read-only, the same data as `tallpine merges`. */
export default function MergesScreen() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const insets = useSafeAreaInsets();
	const { projectId, hostId } = useLocalSearchParams<{ projectId: string; hostId?: string }>();
	const { configForHost, config } = useApp();
	const cfg = hostId ? configForHost(hostId) : config;
	const [digest, setDigest] = useState<MergeDigest>();
	const [failed, setFailed] = useState(false);
	const [refreshing, setRefreshing] = useState(false);

	const load = useCallback(async () => {
		if (!cfg || !projectId) {
			setFailed(true);
			return;
		}
		try {
			setDigest(await getMergeDigest(cfg, projectId));
			setFailed(false);
		} catch {
			setFailed(true);
		}
	}, [cfg, projectId]);
	useEffect(() => { void load(); }, [load]);

	const sections = useMemo(() => (digest ? mergeDigestLanes(digest).flatMap((lane) => [
		...(lane.inFlight.length ? [{ key: `${lane.lane}:open`, label: `${lane.lane} · in flight`, data: lane.inFlight }] : []),
		...(lane.recentlyMerged.length ? [{ key: `${lane.lane}:merged`, label: `${lane.lane} · merged`, data: lane.recentlyMerged }] : []),
	]) : []), [digest]);

	return (
		<View style={styles.screen}>
			<View style={{ height: insets.top }} />
			<ScreenHeader title="Merges" left={<HeaderIconButton icon="back" label="Back" onPress={() => backOr(router, "/projects")} />} />
			{!digest && !failed ? (
				<View style={styles.center}><ActivityIndicator color={t.accent} /></View>
			) : failed && !digest ? (
				<EmptyState icon="wifi-off" title="Couldn't load merges" message="Check the computer is reachable, then try again." action={<Button title="Retry" icon="refresh-cw" onPress={() => void load()} />} />
			) : (
				<SectionList
					sections={sections}
					keyExtractor={({ entry }) => `${entry.number}:${entry.state}`}
					stickySectionHeadersEnabled={false}
					contentContainerStyle={{ paddingBottom: insets.bottom + 40 }}
					refreshControl={<RefreshControl refreshing={refreshing} tintColor={t.accent} onRefresh={async () => { setRefreshing(true); await load(); setRefreshing(false); }} />}
					renderSectionHeader={({ section }) => <ListSectionHeader label={section.label} count={section.data.length} />}
					renderItem={({ item }) => <MergeRow row={item} styles={styles} />}
					ListEmptyComponent={digest && mergeDigestEmpty(digest) ? <EmptyState icon="git-merge" title="No recent merges" message="Nothing merged or in flight for this project." /> : undefined}
				/>
			)}
		</View>
	);
}

function MergeRow({ row, styles }: { row: MergeDigestRow; styles: ReturnType<typeof makeStyles> }) {
	const { entry, overlaps } = row;
	return (
		<View style={styles.row}>
			<Text style={styles.title} numberOfLines={2} accessibilityRole="link" onPress={() => void openGitHub(entry.url)}>#{entry.number} {entry.title}{entry.state === "draft" ? " [draft]" : ""}</Text>
			{overlaps.map((overlap) => <Text key={overlap.with} style={styles.advice}>{overlapLine(overlap)}</Text>)}
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		screen: { flex: 1, backgroundColor: t.bgBase },
		center: { flex: 1, alignItems: "center", justifyContent: "center", paddingVertical: 60 },
		row: { paddingHorizontal: space.lg, paddingVertical: space.sm, gap: space.hair, borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: t.borderSubtle },
		title: { color: t.textPrimary, fontSize: type.callout.fontSize, lineHeight: type.callout.lineHeight },
		advice: { color: t.amber, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight },
	});
