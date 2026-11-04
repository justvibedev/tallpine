import { Feather } from "../../icons";
import { useEffect, useState } from "react";
import { ActivityIndicator, Pressable, Text, View } from "react-native";
import { haptics } from "../../haptics";
import { useTheme, useThemedStyles } from "../../ThemeProvider";
import { Spinning } from "../../ui";
import { FoldChevron, StatusLabel, StatusSpinner } from "../TurnFoldMotion";
import { workingElapsedLabel } from "../conversationChrome";
import type { ConversationTurn } from "../types";
import { press, type } from "../../tokens";
import { useFilesNavigation } from "../../files/navigation";
import { userFacingError } from "../../connectionError";
import { PRODUCT_NAME } from "../../brand";
import { elapsed } from "./format";
import { Action } from "./requestCards";
import { makeStyles } from "./styles";

/**
 * The turn's status row, which sits between the prompt and the work: a ticking
 * "Working · 12s" while the turn runs, then "Worked for 20s" once it settles. When
 * there is folded work the settled row is the toggle that reveals it.
 */
export function TurnStatusRow({ turn, foldable, open, onToggle }: { turn: ConversationTurn; foldable: boolean; open: boolean; onToggle(): void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const running = turn.state === "running";
	const [nowMs, setNowMs] = useState(() => Date.now());
	useEffect(() => {
		if (!running) return;
		setNowMs(Date.now());
		const timer = setInterval(() => setNowMs(Date.now()), 1_000);
		return () => clearInterval(timer);
	}, [running, turn.id]);
	const duration = elapsed(turn.startedAt ?? turn.requestedAt, turn.completedAt);
	const workingDuration = running ? workingElapsedLabel(turn.startedAt ?? turn.requestedAt, nowMs) : undefined;
	const label = turn.rolledBack
		? "Rolled back"
		: turn.state === "completed"
			? duration ? `Worked for ${duration}` : "Work completed"
			: turn.state === "failed"
				? duration ? `Failed after ${duration}` : "Turn failed"
				: turn.state === "interrupted"
					? duration ? `Stopped after ${duration}` : "Turn stopped"
					: workingDuration ? `Working · ${workingDuration}` : "Working";
	const text = <Text style={[styles.turnState, turn.state === "failed" && { color: t.red }]}>{label}</Text>;
	// One element throughout, so the spinner can leave and the label slide into
	// its place when the turn settles instead of the whole row remounting.
	return <Pressable
		accessibilityRole={foldable ? "button" : undefined}
		accessibilityState={foldable ? { expanded: open } : undefined}
		disabled={!foldable}
		onPress={() => { haptics.tap(); onToggle(); }}
		style={styles.statusRow}
	>
		{/* Spinning holds the glyph still under Reduce Motion; loader still reads as in progress. */}
		{running ? <StatusSpinner><Spinning enabled><Feather name="loader" size={12} color={t.textTertiary} /></Spinning></StatusSpinner> : null}
		<StatusLabel>{text}</StatusLabel>
		{foldable ? <FoldChevron open={open} color={t.textFaint} /> : null}
	</Pressable>;
}

export function TurnSummary({ turn, onRollback }: { turn: ConversationTurn; onRollback?(turnId: string): Promise<number> }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [confirming, setConfirming] = useState(false);
	const [rollingBack, setRollingBack] = useState(false);
	const [rollbackError, setRollbackError] = useState<string>();
	const settled = turn.state !== "running" && turn.state !== "queued";
	const canRollBack = Boolean(onRollback && settled && turn.providerTurnId && !turn.rolledBack);
	return (
		<View style={styles.turnWrap}>
			{turn.plan?.steps.length ? <TurnPlan turn={turn} /> : null}
			{turn.diff?.files.length ? <ChangedFiles turn={turn} /> : null}
			{canRollBack ? <View style={styles.turnLine}>
				<Pressable accessibilityLabel="Roll back to before this turn" hitSlop={8} onPress={() => { haptics.warning(); setConfirming(true); }}>
					<Feather name="rotate-ccw" size={12} color={t.textTertiary} />
				</Pressable>
			</View> : null}
			{turn.errorMessage ? <Text style={styles.turnError}>{turn.errorMessage}</Text> : null}
			{confirming ? (
				<View style={styles.rollbackConfirm}>
					<Text style={styles.rollbackTitle}>Make the agent forget this turn and everything after it?</Text>
					<Text style={styles.rollbackCopy}>Files stay changed. Only conversation memory is rolled back, and this cannot be undone.</Text>
					<View style={styles.actions}>
						<Action label="Cancel" disabled={rollingBack} onPress={() => { setRollbackError(undefined); setConfirming(false); }} />
						<Action label={rollingBack ? "Rolling back…" : "Roll back"} disabled={rollingBack} tone="danger" onPress={() => {
							if (!onRollback || rollingBack) return;
							setRollingBack(true);
							setRollbackError(undefined);
							void onRollback(turn.id).then(() => setConfirming(false)).catch((cause) => {
								setRollbackError(userFacingError(cause));
							}).finally(() => setRollingBack(false));
						}} />
					</View>
					{rollbackError ? <Text style={styles.validation}>{rollbackError}</Text> : null}
				</View>
			) : null}
		</View>
	);
}

export function TurnPlan({ turn }: { turn: ConversationTurn }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [open, setOpen] = useState(turn.state === "running");
	const done = turn.plan?.steps.filter((step) => step.status === "completed").length ?? 0;
	return (
		<View style={styles.planCard}>
			<Pressable
				accessibilityRole="button"
				accessibilityState={{ expanded: open }}
				style={styles.planHeader}
				onPress={() => { haptics.tap(); setOpen((value) => !value); }}
				>
				<Feather name="list" size={12} color={t.textTertiary} />
				<Text style={styles.planTitle}>Plan</Text>
				{turn.state === "running" ? <Text style={styles.planLive}>STILL CHANGING</Text> : null}
				<Text style={styles.planCount}>{done}/{turn.plan?.steps.length ?? 0}</Text>
				<Feather name={open ? "chevron-up" : "chevron-down"} size={12} color={t.textTertiary} />
			</Pressable>
			{open ? <View style={styles.planBody}>
				{turn.plan?.explanation ? <Text style={styles.detailCopy}>{turn.plan.explanation}</Text> : null}
				{turn.plan?.steps.map((step, index) => <View key={index} accessibilityLabel={`${step.status.replace("_", " ")}: ${step.text}`} style={styles.planStep}>
					<Feather name={step.status === "completed" ? "check-circle" : step.status === "in_progress" ? "circle" : "circle"} size={15} color={step.status === "completed" ? t.green : step.status === "in_progress" ? t.orange : t.textFaint} />
					<Text style={[styles.planStepText, step.status === "completed" && styles.planDone]}>{step.text}</Text>
					<Text style={styles.planStepState}>{step.status.replace("_", " ")}</Text>
				</View>)}
			</View> : null}
		</View>
	);
}

export function ChangedFiles({ turn }: { turn: ConversationTurn }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const files = useFilesNavigation();
	const [open, setOpen] = useState(false);
	const changed = turn.diff?.files ?? [];
	const additions = changed.reduce((sum, file) => sum + file.additions, 0);
	const deletions = changed.reduce((sum, file) => sum + file.deletions, 0);
	return <View style={styles.planCard}>
		<View style={styles.changedHeader}>
			<Pressable
				accessibilityRole="button"
				accessibilityState={{ expanded: open }}
				accessibilityLabel={`${changed.length} changed ${changed.length === 1 ? "file" : "files"}, ${additions} lines added, ${deletions} removed`}
				style={[styles.planHeader, styles.changedToggle]}
				onPress={() => { haptics.tap(); setOpen((value) => !value); }}
				>
				<Feather name="file-text" size={12} color={t.textTertiary} />
				<Text numberOfLines={1} style={[styles.planTitle, styles.changedTitle]}>{files ? `${changed.length} ${changed.length === 1 ? "file" : "files"}` : `${changed.length} changed ${changed.length === 1 ? "file" : "files"}`}</Text>
				{turn.state === "running" ? <><Text numberOfLines={1} style={styles.planLive}>GROWING</Text><ActivityIndicator size="small" color={t.textTertiary} /></> : null}
				<Text style={{ fontFamily: "Geist_400Regular", color: t.green, fontSize: type.caption2.fontSize }}>+{additions}</Text>
				<Text style={{ fontFamily: "Geist_400Regular", color: t.red, fontSize: type.caption2.fontSize }}>−{deletions}</Text>
				<Feather name={open ? "chevron-up" : "chevron-down"} size={12} color={t.textTertiary} />
			</Pressable>
			{files ? <Pressable
				accessibilityRole="button"
				accessibilityLabel="Review changes"
				accessibilityHint="Opens every changed file in this session"
				hitSlop={6}
				onPress={() => { haptics.tap(); files.openChanges(); }}
				style={({ pressed }) => [styles.changedReview, pressed && { opacity: press.opacity }]}
			>
				<Feather name="file-diff" size={12} color={t.textPrimary} />
				<Text style={styles.changedReviewText}>Review</Text>
			</Pressable> : null}
		</View>
		{open ? <View style={styles.fileBody}>{changed.map((file) => {
			const row = <>
				<Text style={[styles.fileMark, { color: file.status === "deleted" ? t.red : file.status === "added" ? t.green : t.accent }]}>{file.status[0].toUpperCase()}</Text>
				<Text selectable={!files} style={styles.filePath}>{file.oldPath ? `${file.oldPath} → ${file.path}` : file.path}</Text>
				<Text style={styles.fileStat}>+{file.additions} −{file.deletions}</Text>
				{files ? <Feather name="chevron-right" size={12} color={t.textFaint} /> : null}
			</>;
			return files
				? <Pressable
					key={`${file.oldPath}:${file.path}`}
					accessibilityRole="button"
					accessibilityLabel={`${file.path}, ${file.status}, ${file.additions} added, ${file.deletions} removed`}
					accessibilityHint="Opens the diff"
					onPress={() => { haptics.tap(); files.openFile(file.path, file.oldPath); }}
					style={({ pressed }) => [styles.fileRow, styles.fileRowTap, pressed && { opacity: press.opacity }]}
				>{row}</Pressable>
				: <View key={`${file.oldPath}:${file.path}`} style={styles.fileRow}>{row}</View>;
		})}{turn.diff?.truncated ? <Text style={[styles.partial, { color: t.amber }]}>{files ? `This turn changed more files than ${PRODUCT_NAME} lists here. Review changes shows all of them.` : `This turn changed more files than ${PRODUCT_NAME} lists here. Open the worktree shell for the complete diff.`}</Text> : null}</View> : null}
	</View>;
}
