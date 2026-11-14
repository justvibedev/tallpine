import { useState } from "react";
import { Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import type { ServerConfig } from "../config";
import { userFacingError } from "../connectionError";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { textSelectionColors } from "../textSelection";
import { radius, space, type } from "../tokens";
import { Button } from "../ui";
import { answerAsk, type AskAnswerInput } from "./askApi";
import { asksForChat, earlierAsk, laneAsksElsewhere, type OwnerAsk } from "./askModel";
import { useProjectAsks } from "./useProjectAsks";

/**
 * "Waiting on you": the open asks of this orchestrator chat with one-tap
 * answers and Other. A lane's asks show only in its chat; the main chat gets a
 * one-line pointer per lane ask. Folds to its header but never hides while an
 * ask is open; nothing while none is.
 */
export function AskStrip({ cfg, projectId, lane }: { cfg: ServerConfig | null | undefined; projectId: string | undefined; lane?: string }) {
	const s = useThemedStyles(makeStyles);
	const { asks: all, refresh } = useProjectAsks(cfg, projectId);
	const [collapsed, setCollapsed] = useState(false);
	// Outlives the card: an answered ask leaves the strip on refetch, the notice must not.
	const [undelivered, setUndelivered] = useState<string[]>([]);
	const asks = asksForChat(all, lane);
	const elsewhere = laneAsksElsewhere(all, lane);
	if (!cfg || !projectId || (asks.length === 0 && elsewhere.length === 0 && undelivered.length === 0)) return null;
	return (
		<ScrollView testID="ask-strip" style={s.strip} contentContainerStyle={s.content}>
			{elsewhere.map((a) => (
				<Text key={a.id} testID="ask-lane-pointer" style={s.muted}>{`${a.ref} waits on you in lane ${a.lane}`}</Text>
			))}
			{undelivered.map((ref) => (
				<Text key={ref} testID="ask-undelivered" accessibilityRole="alert" style={s.error}>{`Your answer to ${ref} was saved but not delivered to the orchestrator. Send it in the chat.`}</Text>
			))}
			{asks.length > 0 ? (
				<Pressable testID="ask-strip-header" accessibilityRole="button" accessibilityState={{ expanded: !collapsed }} onPress={() => setCollapsed((v) => !v)}>
					<Text style={s.header}>{`Waiting on you · ${asks.length}`}</Text>
				</Pressable>
			) : null}
			{collapsed ? null : asks.map((a) => (
				<AskCard key={a.id} cfg={cfg} projectId={projectId} ask={a} earlier={earlierAsk(all, a)} onChanged={() => void refresh()} onUndelivered={(ref) => setUndelivered((u) => (u.includes(ref) ? u : [...u, ref]))} />
			))}
		</ScrollView>
	);
}

/** One open ask: its options (recommended marked) plus Other. A tap answers; the list refetches after. */
export function AskCard({ cfg, projectId, ask, earlier, onChanged, onUndelivered }: {
	cfg: ServerConfig; projectId: string; ask: OwnerAsk; earlier?: OwnerAsk; onChanged: () => void; onUndelivered: (ref: string) => void;
}) {
	const t = useTheme();
	const s = useThemedStyles(makeStyles);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string>();
	const [other, setOther] = useState<string | null>(null);
	const send = async (input: AskAnswerInput) => {
		setBusy(true);
		setError(undefined);
		try { if ((await answerAsk(cfg, projectId, ask.ref, input)).deliveryFailed) onUndelivered(ask.ref); } catch (e) { setError(userFacingError(e)); } finally { setBusy(false); onChanged(); }
	};
	return (
		<View style={s.card} testID="ask-card">
			<Text style={s.title}>{`${ask.ref} ${ask.text}`}</Text>
			{ask.reason ? <Text style={s.muted}>{ask.reason}</Text> : null}
			{ask.reaskReason ? <Text style={s.muted} testID="ask-reask">{`Asked again: ${ask.reaskReason}`}</Text> : null}
			{earlier?.answer ? <Text style={s.muted} testID="ask-earlier">{`Earlier ${earlier.ref} you answered: ${earlier.answer.text}`}</Text> : null}
			{ask.options.map((o) => (
				<Pressable key={o.id} accessibilityRole="button" accessibilityLabel={o.label} disabled={busy} onPress={() => void send({ option: o.id })} style={s.option}>
					<Text style={s.optionLabel}>{o.label}</Text>
					{o.id === ask.recommended ? <Text style={s.hint}>Recommended</Text> : null}
				</Pressable>
			))}
			<Pressable accessibilityRole="button" accessibilityLabel="Other" disabled={busy} onPress={() => setOther(other === null ? "" : null)} style={s.option}>
				<Text style={s.optionLabel}>Other</Text>
			</Pressable>
			{other !== null ? (
				<>
					<TextInput
						accessibilityLabel={`Your answer to ${ask.text}`}
						value={other}
						editable={!busy}
						placeholder="Type your answer…"
						placeholderTextColor={t.textTertiary}
						{...textSelectionColors(t)}
						onChangeText={setOther}
						style={s.input}
					/>
					<Button title="Send" icon="send" disabled={busy || other.trim() === ""} loading={busy} onPress={() => void send({ other: other.trim() })} />
				</>
			) : null}
			{error ? <Text style={s.error} accessibilityRole="alert">{error}</Text> : null}
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		strip: { maxHeight: 320, flexGrow: 0 },
		content: { gap: space.sm, paddingHorizontal: space.md, paddingBottom: space.sm },
		header: { color: t.textPrimary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, fontWeight: "600" },
		card: { backgroundColor: t.bgElevated, borderRadius: radius.lg, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderSubtle, padding: space.md, gap: space.xxs },
		title: { color: t.textPrimary, fontSize: type.callout.fontSize, lineHeight: type.callout.lineHeight, fontWeight: "600" },
		muted: { color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		error: { color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		option: { flexDirection: "row", alignItems: "center", gap: space.sm, minHeight: 44, paddingVertical: space.sm, paddingHorizontal: space.sm, borderRadius: radius.md, backgroundColor: t.bgElevatedHover },
		optionLabel: { flex: 1, flexShrink: 1, textAlign: "left", color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
		hint: { color: t.textTertiary, fontSize: type.caption1.fontSize },
		input: { minHeight: 44, borderRadius: radius.md, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderSubtle, paddingHorizontal: space.md, color: t.textPrimary, fontSize: type.subheadline.fontSize },
	});
