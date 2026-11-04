import { formatUsageReset } from "@tallpine/shared/format/usage-reset";
import { useState } from "react";
import { Pressable, StyleSheet, Text, TextInput, View } from "react-native";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { textSelectionColors } from "../textSelection";
import { radius, space, type } from "../tokens";
import { Button } from "../ui";
import {
	OTHER_OPTION, PLAN_STATUS_LABEL, acceptedAssumptionCount, chooseOption, correct, initialDraft, isComplete, isDefaults, optionLabel, planPhase,
	toAnswersRequest, writeOther, type PlanAnswersRequest, type PlanDraft, type PlanView,
} from "./planCardModel";
import { PlanReader } from "./PlanReader";

export type PlanCardActions = {
	onAnswer: (request: PlanAnswersRequest) => void;
	onDefaults: () => void;
	onApprove: () => void;
};

/**
 * The owner's surface for one plan on the phone (B205): questions as option
 * rows with the recommended answer preselected plus Other, assumptions accepted
 * unless corrected, Approve, and an explicit "Go with defaults". Nothing times
 * out; waiting is the default.
 */
export function PlanCard({ itemRef, itemTitle, plan, busy = false, error, onAnswer, onDefaults, onApprove }: PlanCardActions & {
	itemRef: string; itemTitle: string; plan: PlanView; busy?: boolean; error?: string;
}) {
	const s = useThemedStyles(makeStyles);
	const phase = planPhase(plan);
	// A planner stopped on its provider's usage limit waits for the reset; it is not writing.
	const resetAt = (phase === "drafting" || phase === "writing") && plan.pausedUntil ? formatUsageReset(plan.pausedUntil, undefined) : null;
	return (
		<View style={s.card} testID="plan-card">
			<View style={s.header}>
				<Text style={s.title} numberOfLines={2}>{itemRef} {itemTitle}</Text>
				<Text style={s.status}>{resetAt ? "Waiting for usage reset" : PLAN_STATUS_LABEL[phase]}</Text>
			</View>
			{phase === "drafting" || phase === "writing" ? (
				<Text style={s.muted}>{resetAt ? `The planner hit its usage limit and picks the plan up again by itself at ${resetAt}.` : phase === "drafting" ? "The planner is reading the code." : "Writing the plan from your answers."}</Text>
			) : null}
			{phase === "answering" ? <AnsweringBody plan={plan} busy={busy} onSend={(draft) => onAnswer(toAnswersRequest(plan, draft))} onDefaults={onDefaults} /> : null}
			{phase === "writing" && plan.answers ? <AnswerSummary plan={plan} /> : null}
			{phase === "review" ? (
				<>
					<PlanReader plan={plan} />
					<View style={s.footer}><Button title="Approve plan" icon="check" disabled={busy} loading={busy} onPress={onApprove} /></View>
				</>
			) : null}
			{phase === "failed" ? <Text style={s.muted}>{plan.failure || "Planning failed."}</Text> : null}
			{error ? <Text style={s.error} accessibilityRole="alert">{error}</Text> : null}
		</View>
	);
}

function AnsweringBody({ plan, busy, onSend, onDefaults }: { plan: PlanView; busy: boolean; onSend: (draft: PlanDraft) => void; onDefaults: () => void }) {
	const t = useTheme();
	const s = useThemedStyles(makeStyles);
	const [draft, setDraft] = useState<PlanDraft>(() => initialDraft(plan));
	const [correcting, setCorrecting] = useState<string | null>(null);
	const complete = isComplete(plan, draft);
	const defaults = isDefaults(plan, draft);
	const accepted = acceptedAssumptionCount(plan, draft);
	return (
		<View style={s.stack}>
			{plan.questions.map((q) => {
				const choice = draft.choices[q.id];
				const options = [
					...q.options.map((o) => ({ id: o.id, label: o.label, hint: o.id === q.recommended ? "Recommended" : undefined })),
					{ id: OTHER_OPTION, label: "Other", hint: undefined },
				];
				return (
					<View key={q.id} style={s.question}>
						<Text style={s.questionText}>{q.text}</Text>
						{q.reason ? <Text style={s.muted}>{q.reason}</Text> : null}
						{options.map((o) => {
							const selected = choice?.selected === o.id;
							return (
								<Pressable
									key={o.id}
									accessibilityRole="radio"
									accessibilityState={{ selected, disabled: busy }}
									accessibilityLabel={o.label}
									disabled={busy}
									onPress={() => setDraft((d) => chooseOption(d, q.id, o.id))}
									style={[s.option, selected && s.optionSelected]}
								>
									<View style={[s.radio, selected && s.radioOn]} />
									<Text style={s.optionLabel}>{o.label}</Text>
									{o.hint ? <Text style={s.hint}>{o.hint}</Text> : null}
								</Pressable>
							);
						})}
						{choice?.selected === OTHER_OPTION ? (
							<TextInput
								accessibilityLabel={`Your answer to ${q.text}`}
								value={choice.other}
								editable={!busy}
								placeholder="Type your answer…"
								placeholderTextColor={t.textTertiary}
								{...textSelectionColors(t)}
								onChangeText={(text) => setDraft((d) => writeOther(d, q.id, text))}
								style={s.input}
							/>
						) : null}
					</View>
				);
			})}
			{plan.assumptions.length > 0 ? (
				<View style={s.question}>
					<Text style={s.heading}>Assumptions (accepted unless you change them)</Text>
					{plan.assumptions.map((a) => {
						const corrected = draft.corrections[a.id];
						const open = correcting === a.id;
						return (
							<View key={a.id} style={[s.assumption, a.scopeCut && corrected === undefined && s.scopeCut]}>
								{a.scopeCut && corrected === undefined ? <Text style={s.scopeTag}>Drops or postpones scope</Text> : null}
								<View style={s.assumptionRow}>
									<Text style={[s.assumptionText, corrected !== undefined && s.struck]}>{a.text}</Text>
									<Pressable accessibilityRole="button" accessibilityLabel={`Change: ${a.text}`} accessibilityState={{ expanded: open }} disabled={busy} onPress={() => setCorrecting(open ? null : a.id)} hitSlop={8}>
										<Text style={s.link}>Change</Text>
									</Pressable>
								</View>
								{open || corrected !== undefined ? (
									<TextInput
										accessibilityLabel={`Correction for ${a.text}`}
										value={corrected ?? ""}
										editable={!busy}
										placeholder="What is true instead?"
										placeholderTextColor={t.textTertiary}
										{...textSelectionColors(t)}
										onChangeText={(text) => setDraft((d) => correct(d, a.id, text))}
										style={s.input}
									/>
								) : null}
							</View>
						);
					})}
				</View>
			) : null}
			{plan.assumptions.length > 0 ? (
				<Text style={s.acceptedNote}>
					{accepted === 1 ? "1 assumption will be accepted as written" : `${accepted} assumptions will be accepted as written`}
				</Text>
			) : null}
			<View style={s.footer}>
				<Button title="Go with defaults" variant="ghost" disabled={busy} onPress={onDefaults} />
				<Button title="Send answers" icon="send" disabled={busy || !complete} loading={busy} onPress={() => (defaults ? onDefaults() : onSend(draft))} />
			</View>
		</View>
	);
}

function AnswerSummary({ plan }: { plan: PlanView }) {
	const s = useThemedStyles(makeStyles);
	const answers = plan.answers;
	if (!answers) return null;
	return (
		<View style={s.stack}>
			{plan.questions.map((q) => {
				const a = answers.questions?.[q.id];
				if (!a) return null;
				return (
					<View key={q.id}>
						<Text style={s.muted}>{q.text}</Text>
						<Text style={s.questionText}>{a.other ? a.other : optionLabel(plan, q.id, a.option ?? "")}</Text>
					</View>
				);
			})}
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		card: { backgroundColor: t.bgElevated, borderRadius: radius.lg, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderSubtle, padding: space.md, gap: space.md },
		header: { gap: space.hair },
		title: { color: t.textPrimary, fontSize: type.callout.fontSize, lineHeight: type.callout.lineHeight, fontWeight: "600" },
		status: { color: t.amber, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight },
		muted: { color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		error: { color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		stack: { gap: space.lg },
		question: { gap: space.xxs },
		questionText: { color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "500" },
		heading: { color: t.textSecondary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, fontWeight: "600" },
		option: { flexDirection: "row", alignItems: "center", gap: space.sm, minHeight: 44, paddingHorizontal: space.sm, borderRadius: radius.md },
		optionSelected: { backgroundColor: t.bgElevatedHover },
		radio: { width: 16, height: 16, borderRadius: 8, borderWidth: 1.5, borderColor: t.textTertiary },
		radioOn: { borderColor: t.accent, backgroundColor: t.accent },
		optionLabel: { flex: 1, color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
		hint: { color: t.textTertiary, fontSize: type.caption1.fontSize },
		input: { minHeight: 44, borderRadius: radius.md, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderSubtle, paddingHorizontal: space.md, color: t.textPrimary, fontSize: type.subheadline.fontSize },
		assumption: { gap: space.xxs },
		scopeCut: { borderLeftWidth: 3, borderLeftColor: t.amber, paddingLeft: space.sm },
		scopeTag: { color: t.amber, fontSize: type.footnote.fontSize, fontWeight: "600" },
		acceptedNote: { color: t.textSecondary, fontSize: type.footnote.fontSize },
		assumptionRow: { flexDirection: "row", alignItems: "flex-start", gap: space.sm },
		assumptionText: { flex: 1, color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
		struck: { textDecorationLine: "line-through", color: t.textSecondary },
		link: { color: t.accent, fontSize: type.subheadline.fontSize },
		footer: { flexDirection: "row", justifyContent: "flex-end", gap: space.sm },
	});
