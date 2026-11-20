import { useState } from "react";
import { StyleSheet, Text, TextInput, View } from "react-native";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { textSelectionColors } from "../textSelection";
import { radius, space, type } from "../tokens";
import { Button } from "../ui";
import { PLAN_COMMENT_OUTCOME_LABEL, planCommentState, type PlanView } from "./planCardModel";
import { PlanReader } from "./PlanReader";

/** Each action takes the comment and a callback that clears the box once the server took it. */
export type PlanReviewActions = {
	onApprove: (comment: string, sent: () => void) => void;
	onReplan: (comment: string, sent: () => void) => void;
	onComment: (text: string, sent: () => void) => void;
};

/**
 * A submitted plan on the phone (B831): the plan, the owner's comments with
 * what the planner made of them, and one comment box sent on its own, with
 * Approve, or with Re-plan. Approve waits while the planner answers a comment.
 */
export function PlanReviewBody({ plan, busy, onApprove, onReplan, onComment }: PlanReviewActions & { plan: PlanView; busy: boolean }) {
	const t = useTheme();
	const s = useThemedStyles(makeStyles);
	const [comment, setComment] = useState("");
	const state = planCommentState(plan);
	const hasText = comment.trim() !== "";
	const sent = () => setComment("");
	const comments = plan.comments ?? [];
	return (
		<>
			<PlanReader plan={plan} />
			{comments.length > 0 ? (
				<View style={s.list}>
					<Text style={s.heading}>Your comments</Text>
					{comments.map((c) => (
						<Text key={c.id} style={s.comment}>{c.text}<Text style={s.muted}> · {PLAN_COMMENT_OUTCOME_LABEL[c.outcome] ?? c.outcome}</Text></Text>
					))}
				</View>
			) : null}
			{state !== "none" ? (
				<Text style={s.muted} accessibilityRole="text">
					{state === "held"
						? "Approved with your comment. The implementer starts once the planner accepts it; if the planner revises the plan or asks questions, you approve again."
						: "The planner is answering your comment. You can approve once it accepts the comment or revises the plan."}
				</Text>
			) : null}
			<TextInput
				accessibilityLabel="Comment on the whole plan"
				value={comment}
				editable={!busy}
				multiline
				placeholder="Comment on the whole plan. Send it alone, or with Re-plan or Approve."
				placeholderTextColor={t.textTertiary}
				{...textSelectionColors(t)}
				onChangeText={setComment}
				style={s.input}
			/>
			<View style={s.footer}>
				<Button title="Comment" variant="ghost" disabled={busy || !hasText} onPress={() => onComment(comment, sent)} />
				<Button title={hasText ? "Re-plan with comment" : "Re-plan"} variant="ghost" disabled={busy} onPress={() => onReplan(comment, sent)} />
				<Button title={hasText ? "Approve with comment" : "Approve plan"} icon="check" disabled={busy || state !== "none"} loading={busy} onPress={() => onApprove(comment, sent)} />
			</View>
		</>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		list: { gap: space.xxs },
		heading: { color: t.textSecondary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, fontWeight: "600" },
		comment: { color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
		muted: { color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		input: { minHeight: 64, borderRadius: radius.md, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderSubtle, paddingHorizontal: space.md, paddingVertical: space.sm, color: t.textPrimary, fontSize: type.subheadline.fontSize, textAlignVertical: "top" },
		footer: { flexDirection: "row", flexWrap: "wrap", justifyContent: "flex-end", gap: space.sm },
	});
