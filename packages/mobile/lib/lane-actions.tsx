import { useCallback, useEffect, useState } from "react";
import { Modal, StyleSheet, Text, TextInput, View } from "react-native";
import { endLane, handoffOrchestrator } from "./api";
import { userFacingError } from "./connectionError";
import { haptics } from "./haptics";
import { canEndLane, canHandoff, endLaneBody, parseLaneRef } from "./laneActions";
import { selectLane } from "./laneSelectionStore";
import type { OrchestratorProjectRow } from "./orchestratorView";
import { useApp } from "./store";
import { textSelectionColors } from "./textSelection";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { fontScaleCap, radius, space, type } from "./tokens";
import { Button } from "./ui";

type Dialog = "new" | "end" | "handoff" | "fresh" | null;

/**
 * New lane, Hand off and End lane for a project's selected orchestrator. These
 * call the daemon's existing /orchestrators routes; the daemon owns every rule
 * (unsaved-work refusal, worker re-homing), the phone only asks and shows.
 */
export function LaneActions({ row, hostId }: { row: OrchestratorProjectRow; hostId: string }) {
	const { configForHost, refreshHost, launchConductor } = useApp();
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [dialog, setDialog] = useState<Dialog>(null);
	const [ref, setRef] = useState("");
	const [workers, setWorkers] = useState<number | null>(null);
	const [error, setError] = useState<string | null>(null);
	const [busy, setBusy] = useState(false);
	// The orchestrator the open dialog acts on, pinned when it opens. `row.link`
	// is rebuilt on every refresh and falls back to another lane once this one is
	// ended, so the dialog must not follow it.
	const [target, setTarget] = useState<{ id: string; lane: string } | null>(null);
	const lane = target?.lane ?? "";
	const targetId = target?.id;
	const open = (next: Dialog) => {
		if (next !== "new") {
			const link = row.link;
			if (!link) return;
			setTarget({ id: link.id, lane: link.lane ?? "" });
		}
		setDialog(next);
	};

	const close = useCallback(() => {
		setDialog(null);
		setTarget(null);
		setRef("");
		setWorkers(null);
		setError(null);
		setBusy(false);
	}, []);

	// Opening End lane runs the dry run first, as desktop does: it names how many
	// workers move, or refuses before anything is touched.
	useEffect(() => {
		if (dialog !== "end" || !targetId) return;
		const config = configForHost(hostId);
		if (!config) {
			setError("This computer is not connected.");
			return;
		}
		let cancelled = false;
		endLane(config, targetId, { dryRun: true }).then(
			(result) => !cancelled && setWorkers(result.workers.length),
			(cause) => !cancelled && setError(userFacingError(cause, "Could not check the lane.")),
		);
		return () => {
			cancelled = true;
		};
	}, [dialog, targetId, configForHost, hostId]);

	const run = async (action: () => Promise<void>) => {
		setBusy(true);
		setError(null);
		try {
			await action();
			haptics.success();
			close();
		} catch (cause) {
			haptics.error();
			setError(userFacingError(cause, "Your computer couldn't complete that."));
			setBusy(false);
			// A refused End lane may already have stopped the orchestrator; show the truth.
			void refreshHost(hostId).catch(() => {});
		}
	};

	const create = () => {
		const parsed = parseLaneRef(ref);
		if (!parsed) return setError("Enter a backlog item such as B12.");
		void run(async () => {
			const created = await launchConductor(row.project.id, false, "chat", hostId, parsed);
			if (created.id) selectLane(hostId, row.project.id, created.id);
		});
	};
	const confirmEnd = () =>
		void run(async () => {
			const config = configForHost(hostId);
			if (!config || !targetId) throw new Error("This computer is not connected.");
			await endLane(config, targetId);
			await refreshHost(hostId);
		});
	// Replaces the live orchestrator with an empty one (no state note): the
	// phone-side equivalent of /clear. The daemon owns the replacement rules.
	const confirmFresh = () =>
		void run(async () => {
			const created = await launchConductor(row.project.id, true, "chat", hostId, target?.lane || undefined);
			await refreshHost(hostId);
			if (created.id) selectLane(hostId, row.project.id, created.id);
		});
	const confirmHandoff = () =>
		void run(async () => {
			const config = configForHost(hostId);
			if (!config || !targetId) throw new Error("This computer is not connected.");
			const next = await handoffOrchestrator(config, targetId);
			await refreshHost(hostId);
			if (next) selectLane(hostId, row.project.id, next);
		});

	return (
		<View style={styles.row}>
			<Button title="New lane" icon="plus" variant="ghost" onPress={() => open("new")} />
			{canHandoff(row.link) ? <Button title="Hand off" icon="refresh-cw" variant="ghost" onPress={() => open("handoff")} /> : null}
			{canHandoff(row.link) ? <Button title="Start fresh" icon="rotate-ccw" variant="ghost" onPress={() => open("fresh")} /> : null}
			{canEndLane(row.link) ? <Button title="End lane" icon="trash-2" variant="ghost" onPress={() => open("end")} /> : null}

			<Modal transparent visible={dialog !== null} animationType="fade" onRequestClose={close}>
				<View style={styles.scrim}>
					<View style={styles.card}>
						{dialog === "new" ? (
							<>
								<Text style={styles.title}>New lane</Text>
								<Text style={styles.body}>Start an orchestrator for a backlog item. Its workers report to it only.</Text>
								<TextInput
									value={ref}
									onChangeText={setRef}
									placeholder="B12"
									placeholderTextColor={t.textFaint}
									{...textSelectionColors(t)}
									autoCapitalize="characters"
									autoCorrect={false}
									accessibilityLabel="Backlog item"
									style={styles.input}
								/>
							</>
						) : null}
						{dialog === "handoff" ? (
							<>
								<Text style={styles.title}>Hand off {lane ? `lane ${lane}` : "the orchestrator"}?</Text>
								<Text style={styles.body}>A fresh orchestrator takes over from the newest state note. This one is replaced.</Text>
							</>
						) : null}
						{dialog === "fresh" ? (
							<>
								<Text style={styles.title}>Start a fresh orchestrator{lane ? ` for ${lane}` : ""}?</Text>
								<Text style={styles.body}>This one is replaced by an empty chat with no memory of this conversation. Use Hand off to keep the state note.</Text>
							</>
						) : null}
						{dialog === "end" ? (
							<>
								<Text style={styles.title}>End lane {lane}?</Text>
								<Text style={styles.body}>{workers === null && !error ? "Checking the lane…" : error && workers === null ? "" : endLaneBody(lane, workers ?? 0)}</Text>
							</>
						) : null}
						{error ? <Text style={styles.error}>{error}</Text> : null}
						<View style={styles.actions}>
							<Button title="Cancel" variant="ghost" onPress={close} disabled={busy} />
							{dialog === "new" ? <Button title="Start lane" loading={busy} onPress={create} /> : null}
							{dialog === "handoff" ? <Button title="Hand off" loading={busy} onPress={confirmHandoff} /> : null}
							{dialog === "fresh" ? <Button title="Start fresh" loading={busy} onPress={confirmFresh} /> : null}
							{dialog === "end" ? <Button title="End lane" variant="danger" loading={busy} disabled={workers === null} onPress={confirmEnd} /> : null}
						</View>
					</View>
				</View>
			</Modal>
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		row: { flexDirection: "row", flexWrap: "wrap", gap: space.xs, paddingHorizontal: space.lg, paddingBottom: space.xs },
		scrim: { flex: 1, justifyContent: "center", padding: space.xl, backgroundColor: t.scrim },
		card: { gap: space.md, padding: space.lg, borderRadius: radius.lg, backgroundColor: t.bgElevated },
		title: { ...type.headline, color: t.textPrimary },
		body: { ...type.footnote, color: t.textSecondary },
		error: { ...type.footnote, color: t.red },
		input: { ...type.body, color: t.textPrimary, padding: space.md, borderRadius: radius.md, backgroundColor: t.bgSubtle },
		actions: { flexDirection: "row", justifyContent: "flex-end", gap: space.sm },
	});
