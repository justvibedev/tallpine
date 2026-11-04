import { useLocalSearchParams, useRouter } from "expo-router";
import { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Alert, FlatList, RefreshControl, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { delegateTask, getBacklog, type BacklogItem } from "../lib/api";
import { backOr } from "../lib/backNavigation";
import { backlogCanDelegate, backlogDelegateRequest, backlogProgress } from "../lib/backlogView";
import { haptics } from "../lib/haptics";
import { useApp } from "../lib/store";
import type { Theme } from "../lib/theme";
import { LivePlanCard, useProjectPlans } from "../lib/plan/PlanCards";
import type { ServerConfig } from "../lib/config";
import type { ProjectPlan } from "../lib/plan/planCardModel";
import { ApprovedPlan } from "../lib/plan/ApprovedPlan";
import { useTheme, useThemedStyles } from "../lib/ThemeProvider";
import { space, type } from "../lib/tokens";
import { Button, EmptyState, HeaderIconButton, ScreenHeader } from "../lib/ui";

export { RouteErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";

/** The project backlog, read-only, with "delegate" sending an item to the default orchestrator. */
export default function BacklogScreen() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const insets = useSafeAreaInsets();
	const { projectId, hostId } = useLocalSearchParams<{ projectId: string; hostId?: string }>();
	const { configForHost, config, refresh } = useApp();
	const cfg = hostId ? configForHost(hostId) : config;
	const [items, setItems] = useState<BacklogItem[]>();
	const [failed, setFailed] = useState(false);
	const [refreshing, setRefreshing] = useState(false);
	const [sending, setSending] = useState<string>();
	const { plans, refresh: refreshPlans } = useProjectPlans(cfg, projectId);
	const [sent, setSent] = useState<Set<string>>(new Set());

	const load = useCallback(async () => {
		if (!cfg || !projectId) {
			setFailed(true);
			return;
		}
		try {
			setItems(await getBacklog(cfg, projectId));
			setFailed(false);
		} catch {
			setFailed(true);
		}
	}, [cfg, projectId]);
	useEffect(() => { void load(); }, [load]);

	const delegate = (item: BacklogItem) => {
		if (!cfg || !projectId || sending) return;
		Alert.alert(`Delegate ${item.ref}?`, "The orchestrator will start a worker on this item.", [
			{ text: "Cancel", style: "cancel" },
			{
				text: "Delegate",
				onPress: async () => {
					setSending(item.ref);
					try {
						await delegateTask(cfg, {
							...backlogDelegateRequest(projectId, item),
							onWarnings: (warnings) => Alert.alert("Started with warnings", warnings.join("\n")),
						});
						haptics.success();
						void refresh();
						setSent((prev) => new Set(prev).add(item.ref));
					} catch (e) {
						Alert.alert("Couldn't delegate", e instanceof Error ? e.message : "Try again.");
					} finally {
						setSending(undefined);
					}
				},
			},
		]);
	};

	return (
		<View style={styles.screen}>
			<View style={{ height: insets.top }} />
			<ScreenHeader title="Backlog" left={<HeaderIconButton icon="back" label="Back" onPress={() => backOr(router, "/projects")} />} />
			{!items && !failed ? (
				<View style={styles.center}><ActivityIndicator color={t.accent} /></View>
			) : failed && !items ? (
				<EmptyState icon="wifi-off" title="Couldn't load the backlog" message="Check the computer is reachable, then try again." action={<Button title="Retry" icon="refresh-cw" onPress={() => void load()} />} />
			) : (
				<FlatList
					data={items}
					keyExtractor={(item) => item.ref}
					contentContainerStyle={{ paddingBottom: insets.bottom + 40 }}
					refreshControl={<RefreshControl refreshing={refreshing} tintColor={t.accent} onRefresh={async () => { setRefreshing(true); await load(); setRefreshing(false); }} />}
					ListEmptyComponent={<EmptyState icon="list" title="Backlog is empty" message="No open items for this project." />}
					renderItem={({ item }) => (
						<View style={styles.row}>
							<Text style={styles.title} numberOfLines={2}>{item.ref} {item.title}</Text>
							{item.hold.trim() ? <Text style={styles.hold}>Held: {item.hold}</Text> : null}
							{backlogProgress(item) ? <Text style={styles.meta}>{backlogProgress(item)}</Text> : null}
							{backlogCanDelegate(item) ? (
								<Button
									title={sent.has(item.ref) ? "Delegated" : sending === item.ref ? "Delegating…" : "Delegate"}
									icon="send"
									variant="ghost"
									disabled={sending !== undefined || sent.has(item.ref)}
									onPress={() => delegate(item)}
								/>
							) : null}
							{cfg && projectId ? <ItemPlan cfg={cfg} projectId={projectId} item={item} waiting={plans.find((p) => p.itemRef === item.ref)} onChanged={() => { void refreshPlans(); void load(); }} /> : null}
						</View>
					)}
				/>
			)}
		</View>
	);
}

function ItemPlan({ cfg, projectId, item, waiting, onChanged }: { cfg: ServerConfig; projectId: string; item: BacklogItem; waiting?: ProjectPlan; onChanged: () => void }) {
	if (waiting) return <LivePlanCard cfg={cfg} projectId={projectId} itemRef={item.ref} itemTitle={item.title} plan={waiting.plan} onChanged={onChanged} />;
	return <ApprovedPlan cfg={cfg} projectId={projectId} itemRef={item.ref} />;
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		screen: { flex: 1, backgroundColor: t.bgBase },
		center: { flex: 1, alignItems: "center", justifyContent: "center", paddingVertical: 60 },
		row: { paddingHorizontal: space.lg, paddingVertical: space.sm, gap: space.hair, borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: t.borderSubtle },
		title: { color: t.textPrimary, fontSize: type.callout.fontSize, lineHeight: type.callout.lineHeight },
		hold: { color: t.amber, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight },
		meta: { color: t.textTertiary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight },
	});
