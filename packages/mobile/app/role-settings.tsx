import { useLocalSearchParams, useRouter } from "expo-router";
import { useCallback, useEffect, useMemo, useState } from "react";
import { ActivityIndicator, Alert, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { rankAgents } from "../lib/agentPicker";
import { getAgentModels, getAgents, getProject, setProjectRoleSettings, type AgentCatalog, type AgentModelCatalog, type ProjectDetail, type RoleKey } from "../lib/api";
import { backOr } from "../lib/backNavigation";
import { userFacingError } from "../lib/connectionError";
import { haptics } from "../lib/haptics";
import { ROLE_KEYS, ROLE_TITLES, allowedAgents, effectiveAgent, setRoleField, effortAfterModelChange, effortOptions, roleSettingsPatch, roleValueLabel, roleValuesFromProject, type RoleSettingsValues } from "../lib/roleSettingsView";
import { agentSheetRoute, modelSheetRoute } from "../lib/sheetResult";
import { useApp } from "../lib/store";
import type { Theme } from "../lib/theme";
import { useTheme, useThemedStyles } from "../lib/ThemeProvider";
import { press, space, type } from "../lib/tokens";
import { Button, EmptyState, HeaderIconButton, ScreenHeader, SettingsGroup, SettingsRow } from "../lib/ui";

export { RouteErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";

/**
 * A project's agent, model and effort for the orchestrator, worker and reviewer
 * roles. Saved on the computer through the narrow role-settings route; env,
 * post-create and the rest of the project config stay on the desktop.
 */
export default function RoleSettingsScreen() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const insets = useSafeAreaInsets();
	const { projectId = "", hostId } = useLocalSearchParams<{ projectId?: string; hostId?: string }>();
	const { configForHost, config, refresh } = useApp();
	const cfg = hostId ? configForHost(hostId) : config;
	const [project, setProject] = useState<ProjectDetail>();
	const [values, setValues] = useState<RoleSettingsValues>();
	const [saved, setSaved] = useState<RoleSettingsValues>();
	const [agents, setAgents] = useState<AgentCatalog>();
	const [catalogs, setCatalogs] = useState<Record<string, AgentModelCatalog>>({});
	const [loadError, setLoadError] = useState<string>();
	const [saving, setSaving] = useState(false);

	const load = useCallback(async () => {
		if (!cfg || !projectId) return;
		setLoadError(undefined);
		try {
			const detail = await getProject(cfg, projectId);
			const next = roleValuesFromProject(detail);
			setProject(detail);
			setSaved(next);
			setValues(next);
		} catch (e) {
			setLoadError(userFacingError(e));
		}
		getAgents(cfg).then(setAgents).catch(() => undefined);
	}, [cfg, projectId]);
	useEffect(() => { void load(); }, [load]);

	const agentIds = useMemo(() => rankAgents(agents).filter((a) => a.selectable).map((a) => a.id), [agents]);
	const patch = useMemo(() => (saved && values ? roleSettingsPatch(saved, values) : undefined), [saved, values]);

	// Model catalogs are per agent; the effort chips read the chosen model's levels.
	const catalogFor = (agent: string) => catalogs[agent];
	useEffect(() => {
		if (!cfg || !values) return;
		for (const role of ROLE_KEYS) {
			const agent = effectiveAgent(role, values);
			if (!agent || catalogs[agent]) continue;
			getAgentModels(cfg, agent, projectId).then((c) => setCatalogs((prev) => ({ ...prev, [agent]: c }))).catch(() => undefined);
		}
	}, [cfg, values, projectId, catalogs]);

	const setField = (role: RoleKey, field: "agent" | "model" | "effort", value: string) =>
		setValues((prev) => prev && setRoleField(prev, role, field, value));

	const save = async () => {
		if (!cfg || !patch || saving) return;
		setSaving(true);
		try {
			const updated = await setProjectRoleSettings(cfg, projectId, patch);
			const next = roleValuesFromProject(updated);
			setProject(updated);
			setSaved(next);
			setValues(next);
			haptics.success();
			void refresh();
		} catch (e) {
			haptics.error();
			Alert.alert("Couldn't save", e instanceof Error && e.name === "RoleSettingsUnsupportedError" ? e.message : userFacingError(e));
		} finally {
			setSaving(false);
		}
	};

	return (
		<View style={styles.screen}>
			<View style={{ height: insets.top }} />
			<ScreenHeader title="Agent settings" left={<HeaderIconButton icon="back" label="Back" onPress={() => backOr(router, "/projects")} />} />
			{!values ? (
				loadError ? (
					<EmptyState icon="wifi-off" title="Couldn't load the project" message={loadError} action={<Button title="Retry" icon="refresh-cw" onPress={() => void load()} />} />
				) : (
					<View style={styles.center}><ActivityIndicator color={t.accent} /></View>
				)
			) : (
				<ScrollView contentContainerStyle={{ paddingBottom: insets.bottom + 40 }}>
					<Text style={styles.intro}>{project?.name}: the agent, model and effort each role starts with. The next session uses the change.</Text>
					{ROLE_KEYS.filter((role) => role !== "reviewer" || project?.kind !== "scratch").map((role) => {
						const v = values[role];
						const agentForRole = effectiveAgent(role, values);
						const modelInfo = catalogFor(agentForRole)?.models.find((m) => m.id === v.model);
						const efforts = effortOptions(agentForRole, modelInfo?.efforts);
						return (
							<SettingsGroup key={role} title={ROLE_TITLES[role]}>
								<SettingsRow
									icon="cpu"
									label="Agent"
									value={roleValueLabel(v.agent)}
									onPress={() => router.push(agentSheetRoute({ selected: v.agent, allowed: allowedAgents(role, agentIds), onSelect: (id) => setField(role, "agent", id) }))}
								/>
								<SettingsRow
									icon="sliders"
									label="Model"
									value={roleValueLabel(v.model)}
									disabled={!agentForRole}
									onPress={() => router.push(modelSheetRoute({ agentId: agentForRole, projectId, selected: v.model, onSelect: (id) => { const model = id === "__auto__" ? "" : id; setField(role, "model", model); const efforts = catalogFor(agentForRole)?.models.find((m) => m.id === model)?.efforts; const kept = effortAfterModelChange(v.effort, efforts); if (kept !== v.effort) setField(role, "effort", kept); } }))}
								/>
								{efforts.length > 0 ? (
									<View style={styles.effortRow}>
										<Text style={styles.effortLabel}>Effort</Text>
										<View style={styles.chips}>
											{["", ...efforts].map((level) => (
												<Pressable
													key={level || "default"}
													accessibilityRole="radio"
													accessibilityState={{ selected: v.effort === level }}
													onPress={() => { haptics.select(); setField(role, "effort", level); }}
													style={({ pressed }) => [styles.chip, v.effort === level && styles.chipOn, pressed && { opacity: press.opacity }]}
												>
													<Text style={[styles.chipText, v.effort === level && { color: t.onAccent }]}>{level || "Default"}</Text>
												</Pressable>
											))}
										</View>
									</View>
								) : null}
							</SettingsGroup>
						);
					})}
					<View style={styles.save}>
						<Button title={saving ? "Saving…" : "Save"} icon="check" disabled={!patch || saving} loading={saving} onPress={() => void save()} />
					</View>
				</ScrollView>
			)}
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		screen: { flex: 1, backgroundColor: t.bgBase },
		center: { flex: 1, alignItems: "center", justifyContent: "center" },
		intro: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, paddingHorizontal: space.lg, paddingVertical: space.md },
		effortRow: { paddingHorizontal: space.lg, paddingVertical: space.md, gap: space.sm },
		effortLabel: { fontFamily: "Geist_500Medium", color: t.textPrimary, fontSize: type.subheadline.fontSize },
		chips: { flexDirection: "row", flexWrap: "wrap", gap: space.xs },
		chip: { paddingHorizontal: space.md, paddingVertical: space.xs, borderRadius: 999, borderWidth: 1, borderColor: t.borderDefault, backgroundColor: t.bgElevated },
		chipOn: { backgroundColor: t.accent, borderColor: t.accent },
		chipText: { fontFamily: "Geist_500Medium", color: t.textSecondary, fontSize: type.footnote.fontSize },
		save: { paddingHorizontal: space.lg, paddingTop: space.lg },
	});
