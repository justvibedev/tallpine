import { Feather } from "../../icons";
import * as Clipboard from "expo-clipboard";
import { useState } from "react";
import { ActivityIndicator, Pressable, Text, View } from "react-native";
import { haptics } from "../../haptics";
import { useTheme, useThemedStyles } from "../../ThemeProvider";
import { HighlightedCodeText } from "../HighlightedCodeText";
import { caretNotation, commandOutputText } from "../ansi";
import { providerErrorCopy } from "../providerError";
import type { ConversationActivity } from "../types";
import { activityHierarchy, activityNodesRunning, activityStartsExpanded, countActivityNodes, summarizeActivities, type ActivityNode } from "../timelineModel";
import { PRODUCT_NAME } from "../../brand";
import { activityMeta, fileChanges, formatTokens, lastLine, printable, reviewPaths, truncationNote } from "./format";
import { makeStyles } from "./styles";

export function ActivityRow({ activity }: { activity: ConversationActivity }) {
	if (activity.activityKind === "mcp_tool") return <McpToolRow activity={activity} />;
	if (activity.activityKind === "auto_review") return <AutoReviewRow activity={activity} />;
	if (activity.activityKind === "file_change") return <FileChangeActivity activity={activity} />;
	if (activity.activityKind === "plan") return <PlanActivity activity={activity} />;
	return <GenericActivityRow activity={activity} />;
}

export function GenericActivityRow({ activity }: { activity: ConversationActivity }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const detail = activity.detail ?? {};
	const output = commandOutputText(detail.output ?? detail.result ?? detail.error ?? detail.patchOutput);
	const [openOverride, setOpenOverride] = useState<boolean | null>(null);
	const open = openOverride ?? activityStartsExpanded(activity);
	const expandable = Boolean(output || detail.cwd || detail.arguments || detail.files || detail.reason || detail.text || detail.terminalInput);
	const meta = activityMeta(activity);
	return (
		<View style={styles.activityWrap}>
			<Pressable
				accessibilityRole={expandable ? "button" : undefined}
				accessibilityState={expandable ? { expanded: open } : undefined}
				disabled={!expandable}
				onPress={() => { haptics.tap(); setOpenOverride(!open); }}
				style={styles.activityRow}
			>
				<Feather name={meta.icon} size={12} color={meta.color(t)} />
				<Text numberOfLines={open ? undefined : 2} style={[styles.activitySummary, activity.status === "failed" && { color: t.red }]}>
					{meta.prefix ? `${meta.prefix} ` : ""}{detail.command || detail.toolName || activity.summary}
				</Text>
				{activity.status === "running" ? <ActivityIndicator size="small" color={t.orange} /> : null}
				{activity.status === "cancelled" ? <Text style={styles.activityStopped}>stopped</Text> : null}
				{expandable ? <Feather name={open ? "chevron-up" : "chevron-right"} size={12} color={t.textFaint} /> : null}
			</Pressable>
			{open ? (
				<View style={styles.activityDetail}>
					{detail.cwd ? <LabelValue label="cwd" value={detail.cwd} /> : null}
					{detail.reason || detail.text ? <Text style={styles.detailCopy}>{detail.reason ?? detail.text}</Text> : null}
					{detail.arguments !== undefined ? <CodeOutput value={printable(detail.arguments)} /> : null}
					{detail.terminalInput ? <TerminalInput text={detail.terminalInput} truncated={detail.terminalInputTruncated} /> : null}
					{output ? <CodeOutput value={output} /> : null}
					{detail.outputTruncated || detail.patchOutputTruncated ? <Text style={[styles.partial, { color: t.amber }]}>This output is longer than {PRODUCT_NAME} stores, so it stops early. Open the worktree shell for the full run.</Text> : detail.outputMayBePartial ? <Text style={styles.partial}>{detail.outputSource === "stream" ? "Streamed live; the provider may have omitted the beginning." : "The provider's rolled-up output may omit the beginning."} Open the worktree shell for the full run.</Text> : null}
					{Array.isArray(detail.files) ? <FileList files={detail.files} /> : null}
				</View>
			) : null}
		</View>
	);
}

export function TerminalInput({ text, truncated }: { text: string; truncated?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return <View style={styles.terminalInput}><View style={styles.terminalInputTitle}><Feather name="corner-down-right" size={12} color={t.textTertiary} /><Text style={styles.detailLabel}>AGENT TYPED</Text></View><CodeOutput value={caretNotation(text)} />{truncated ? <Text style={styles.partial}>{PRODUCT_NAME} stopped recording keystrokes at its cap; more were sent.</Text> : null}</View>;
}

export function McpToolRow({ activity }: { activity: ConversationActivity }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [open, setOpen] = useState(activity.status === "failed");
	const detail = activity.detail ?? {};
	const failed = activity.status === "failed" || detail.success === false || Boolean(detail.error);
	const body = detail.arguments !== undefined || detail.result !== undefined || Boolean(detail.error || detail.progress);
	return <View style={styles.activityWrap}>
		<Pressable disabled={!body} accessibilityRole={body ? "button" : undefined} accessibilityState={body ? { expanded: open } : undefined} onPress={() => { haptics.tap(); setOpen((value) => !value); }} style={styles.activityRow}>
			<Feather name="tool" size={12} color={failed ? t.red : t.textSecondary} />
			<Text style={[styles.server, failed && { color: t.red }]}>{detail.server ?? detail.namespace ? `${detail.server ?? detail.namespace}/` : ""}</Text>
			<Text numberOfLines={1} style={[styles.activitySummary, failed && { color: t.red }]}>{detail.toolName || activity.summary}</Text>
			{detail.progress ? <Text numberOfLines={1} style={styles.activityProgress}>{lastLine(detail.progress)}</Text> : null}
			{activity.status === "running" ? <ActivityIndicator size="small" color={t.orange} /> : activity.status === "cancelled" ? <Text style={styles.activityStopped}>stopped</Text> : body ? <Feather name={open ? "chevron-up" : "chevron-right"} size={12} color={t.textFaint} /> : null}
		</Pressable>
		{open && body ? <View style={styles.activityDetail}>{detail.error ? <Text style={[styles.detailCopy, { color: t.red }]}>{detail.error}</Text> : null}{detail.arguments !== undefined ? <JsonPayload label="Arguments" value={detail.arguments} /> : null}{detail.result !== undefined ? <JsonPayload label="Result" value={detail.result} /> : null}{detail.progress ? <View><Text style={styles.detailLabel}>PROGRESS</Text><CodeOutput value={detail.progress} />{detail.progressTruncated ? <Text style={styles.partial}>Progress was longer than {PRODUCT_NAME} stores.</Text> : null}</View> : null}</View> : null}
	</View>;
}

export function JsonPayload({ label, value }: { label: string; value: unknown }) {
	const styles = useThemedStyles(makeStyles);
	const note = truncationNote(value);
	return <View><Text style={styles.detailLabel}>{label.toUpperCase()}</Text>{note ? <Text style={styles.detailCopy}>{note}</Text> : <CodeOutput value={printable(value)} />}</View>;
}

export function AutoReviewRow({ activity }: { activity: ConversationActivity }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [open, setOpen] = useState(false);
	const detail = activity.detail ?? {};
	const denied = String(detail.status ?? "").toLowerCase().includes("den");
	const paths = reviewPaths(detail.files);
	const body = Boolean(detail.rationale || detail.command || detail.cwd || detail.host || detail.decisionSource || paths.length);
	return <View style={styles.activityWrap}><Pressable disabled={!body} accessibilityRole={body ? "button" : undefined} accessibilityState={body ? { expanded: open } : undefined} onPress={() => { haptics.tap(); setOpen((value) => !value); }} style={styles.activityRow}><Feather name={denied ? "shield-off" : "shield"} size={12} color={denied ? t.red : t.green} /><Text style={[styles.reviewDecision, denied && { color: t.red }]}>{denied ? "Auto-declined" : "Auto-approved"}</Text><Text numberOfLines={1} style={styles.activitySummary}>{activity.summary}</Text>{detail.riskLevel ? <Text style={[styles.risk, ["high", "critical"].includes(detail.riskLevel.toLowerCase()) && { color: t.red }]}>{detail.riskLevel}</Text> : null}{body ? <Feather name={open ? "chevron-up" : "chevron-right"} size={12} color={t.textFaint} /> : null}</Pressable>{open && body ? <View style={styles.activityDetail}><Text style={styles.detailCopy}>{denied ? "The provider declined this on your behalf. You were not asked." : "The provider allowed this on your behalf. You were not asked."}</Text>{detail.rationale ? <Text style={styles.reviewRationale}>{detail.rationale}</Text> : null}{detail.command ? <LabelValue label="cmd" value={detail.command} /> : null}{detail.cwd ? <LabelValue label="cwd" value={detail.cwd} /> : null}{detail.host ? <LabelValue label="host" value={detail.host} /> : null}{paths.length ? <LabelValue label="files" value={paths.join(", ")} /> : null}{detail.decisionSource ? <LabelValue label="by" value={detail.decisionSource} /> : null}</View> : null}</View>;
}

export function FileChangeActivity({ activity }: { activity: ConversationActivity }) {
	const files = fileChanges(activity.detail?.files);
	return <View style={useThemedStyles(makeStyles).activityWrap}><ExpandableFileList title={activity.summary || `${files.length} changed files`} files={files} fallbackPatch={activity.detail?.patchOutput} fallbackPatchTruncated={activity.detail?.patchOutputTruncated} live={activity.status === "running"} /></View>;
}

export function ExpandableFileList({ title, files, fallbackPatch, fallbackPatchTruncated, live }: { title: string; files: ReturnType<typeof fileChanges>; fallbackPatch?: string; fallbackPatchTruncated?: boolean; live?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [openOverride, setOpenOverride] = useState<boolean | null>(null);
	const open = openOverride ?? Boolean(live && (fallbackPatch || files.some((file) => file.patch)));
	const expandable = files.length > 0 || Boolean(fallbackPatch);
	return <View><Pressable disabled={!expandable} accessibilityRole={expandable ? "button" : undefined} accessibilityState={expandable ? { expanded: open } : undefined} onPress={() => { haptics.tap(); setOpenOverride(!open); }} style={styles.activityRow}><Feather name="edit-3" size={12} color={t.accent} /><Text numberOfLines={2} style={styles.activitySummary}>{title}</Text>{expandable ? <Feather name={open ? "chevron-up" : "chevron-right"} size={12} color={t.textFaint} /> : null}</Pressable>{open ? <View style={styles.activityDetail}>{files.map((file) => <FileChangeRow key={`${file.oldPath ?? ""}:${file.path}`} file={file} live={live} />)}{fallbackPatch ? <PatchBlock patch={fallbackPatch} truncated={fallbackPatchTruncated} /> : null}</View> : null}</View>;
}

export function FileChangeRow({ file, live }: { file: ReturnType<typeof fileChanges>[number]; live?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [open, setOpen] = useState(Boolean(live && file.patch));
	const hasPatch = Boolean(file.patch);
	const mark = file.status === "added" ? "A" : file.status === "deleted" ? "D" : file.status === "renamed" ? "R" : "M";
	return <View><Pressable disabled={!hasPatch} accessibilityRole={hasPatch ? "button" : undefined} accessibilityState={hasPatch ? { expanded: open } : undefined} onPress={() => { haptics.tap(); setOpen((value) => !value); }} style={styles.fileRow}><Text style={[styles.fileMark, { color: file.status === "deleted" ? t.red : file.status === "added" ? t.green : t.accent }]}>{mark}</Text><Text selectable numberOfLines={2} style={styles.filePath}>{file.oldPath ? `${file.oldPath} → ${file.path}` : file.path}</Text><Text style={styles.fileStat}>+{file.additions} −{file.deletions}</Text>{hasPatch ? <Feather name={open ? "chevron-up" : "chevron-right"} size={12} color={t.textFaint} /> : null}</Pressable>{open && file.patch ? <PatchBlock patch={file.patch} truncated={file.patchTruncated} /> : null}</View>;
}

export function PatchBlock({ patch, truncated }: { patch: string; truncated?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return <View><Pressable onLongPress={() => { void Clipboard.setStringAsync(patch); haptics.success(); }}><HighlightedCodeText code={patch} language="diff" style={styles.output} /></Pressable>{truncated ? <Text style={[styles.partial, { color: t.amber }]}>This patch is longer than {PRODUCT_NAME} stores. The complete change remains in the worktree.</Text> : null}</View>;
}

export function PlanActivity({ activity }: { activity: ConversationActivity }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [open, setOpen] = useState(activity.status === "running");
	const steps = activity.detail?.steps ?? [];
	return <View style={styles.planCard}><Pressable
			accessibilityRole="button"
			accessibilityState={{ expanded: open }}
			style={styles.planHeader}
			onPress={() => { haptics.tap(); setOpen((value) => !value); }}
			><Feather name="list" size={12} color={t.textTertiary} /><Text style={styles.planTitle}>{activity.summary || "Plan updated"}</Text><Text style={styles.planCount}>{steps.filter((step) => step.status === "completed").length}/{steps.length}</Text><Feather name={open ? "chevron-up" : "chevron-down"} size={12} color={t.textTertiary} /></Pressable>{open ? <View style={styles.planBody}>{activity.detail?.explanation ? <Text style={styles.detailCopy}>{activity.detail.explanation}</Text> : null}{steps.map((step, index) => <View key={index} style={styles.planStep}><Feather name={step.status === "completed" ? "check-circle" : "circle"} size={15} color={step.status === "completed" ? t.green : step.status === "in_progress" ? t.orange : t.textFaint} /><Text style={[styles.planStepText, step.status === "completed" && styles.planDone]}>{step.text}</Text></View>)}{!steps.length ? <Text style={styles.detailCopy}>{activity.detail?.text || activity.summary}</Text> : null}</View> : null}</View>;
}

export function ActivityRun({ activities }: { activities: ConversationActivity[] }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [override, setOverride] = useState<boolean | null>(null);
	const hierarchy = activityHierarchy(activities);
	if (activities.length === 1 && hierarchy[0]?.children.length === 0) return <ActivityRow activity={activities[0]} />;
	const running = activities.some((activity) => activity.status === "running");
	const failed = activities.filter((activity) => activity.status === "failed").length;
	const cancelled = activities.filter((activity) => activity.status === "cancelled").length;
	const streaming = activities.some((activity) => activity.status === "running" && Boolean(activity.detail?.output));
	const open = override ?? streaming;
	return <View style={styles.runWrap}>
		<Pressable
			accessibilityRole="button"
			accessibilityState={{ expanded: open }}
			onPress={() => { haptics.tap(); setOverride(!open); }}
			style={styles.runSummary}
		>
			<Text style={styles.runText}>{summarizeActivities(activities)}</Text>
			{failed ? <Text style={styles.runFailed}>{failed} failed</Text> : null}
			{cancelled ? <Text style={styles.runStopped}>{cancelled} stopped</Text> : null}
			{running ? <ActivityIndicator size="small" color={t.textTertiary} /> : null}
			<Feather name={open ? "chevron-down" : "chevron-right"} size={12} color={t.textFaint} />
		</Pressable>
		{open ? <View style={styles.runDetail}>{hierarchy.map((node) => <ActivityTree key={node.activity.id} node={node} />)}</View> : null}
	</View>;
}

export function ActivityTree({ node }: { node: ActivityNode }) {
	const styles = useThemedStyles(makeStyles);
	return <View><ActivityRow activity={node.activity} />{node.children.length ? <NestedAgentRun nodes={node.children} /> : null}</View>;
}

export function NestedAgentRun({ nodes }: { nodes: ActivityNode[] }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [open, setOpen] = useState(false);
	const count = countActivityNodes(nodes);
	const running = activityNodesRunning(nodes);
	return <View style={styles.subagent}>
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={`${open ? "Hide" : "Show"} subagent work, ${count} ${count === 1 ? "step" : "steps"}`}
			accessibilityState={{ expanded: open }}
			onPress={() => { haptics.tap(); setOpen((value) => !value); }}
			style={styles.subagentHeader}
		>
			<Feather name="git-branch" size={12} color={t.textTertiary} />
			<Text style={styles.subagentLabel}>SUBAGENT · {count} {count === 1 ? "STEP" : "STEPS"}</Text>
			{running ? <ActivityIndicator size="small" color={t.textTertiary} /> : null}
			<Feather name={open ? "chevron-down" : "chevron-right"} size={12} color={t.textFaint} />
		</Pressable>
		{open ? nodes.map((child) => <ActivityTree key={child.activity.id} node={child} />) : null}
	</View>;
}

export function CompactionMarker({ activity }: { activity: ConversationActivity }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const after = activity.detail?.tokensAfter;
	const window = activity.detail?.contextWindow;
	return <View style={styles.compaction}><Feather name="archive" size={12} color={t.textFaint} /><Text style={styles.compactionText}>HISTORY COMPACTED{activity.detail?.tokensReclaimed ? `  −${formatTokens(activity.detail.tokensReclaimed)}` : ""}{after && window ? `  ${Math.round((after / window) * 100)}% FULL` : ""}</Text></View>;
}

export function ErrorActivity({ activity }: { activity: ConversationActivity }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	// Providers set several fields to the same sentence — Codex sends the
	// usage-limit text as both summary and detail.error — so rendering each in
	// turn printed one failure twice inside this card. Same rule as the renderer.
	const { headline, detail } = providerErrorCopy(activity);
	return <View style={[styles.errorCard, { borderColor: t.tintRed }]}><Feather name="alert-triangle" size={15} color={t.red} /><View style={{ flex: 1 }}><Text style={styles.errorTitle}>{headline}</Text>{detail ? <Text selectable style={styles.errorCopy}>{detail}</Text> : null}</View></View>;
}

export function LabelValue({ label, value }: { label: string; value: string }) { const styles = useThemedStyles(makeStyles); return <View style={styles.labelValue}><Text style={styles.detailLabel}>{label}</Text><Text selectable style={styles.detailValue}>{value}</Text></View>; }
export function CodeOutput({ value }: { value: string }) { const styles = useThemedStyles(makeStyles); return <Pressable onLongPress={() => { void Clipboard.setStringAsync(value); haptics.success(); }}><Text selectable style={styles.output}>{value}</Text></Pressable>; }
export function FileList({ files }: { files: unknown[] }) { const styles = useThemedStyles(makeStyles); return <View style={styles.fileBody}>{files.map((file, index) => <Text key={index} selectable style={styles.filePath}>• {typeof file === "string" ? file : printable(file)}</Text>)}</View>; }
