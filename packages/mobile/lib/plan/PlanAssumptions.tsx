import { useState } from "react";
import { Pressable, StyleSheet, Text, TextInput, View } from "react-native";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { textSelectionColors } from "../textSelection";
import { radius, space, type } from "../tokens";
import { correct, type PlanDraft, type PlanView } from "./planCardModel";

/**
 * The planner's assumptions on the phone, collapsed by default (B831). Each is
 * accepted unless corrected; a collapsed header still says when one cuts scope.
 */
export function PlanAssumptions({ plan, draft, busy, setDraft }: {
	plan: PlanView; draft: PlanDraft; busy: boolean;
	setDraft: (change: (d: PlanDraft) => PlanDraft) => void;
}) {
	const t = useTheme();
	const s = useThemedStyles(makeStyles);
	const [open, setOpen] = useState(false);
	const [correcting, setCorrecting] = useState<string | null>(null);
	if (plan.assumptions.length === 0) return null;
	const scopeCuts = plan.assumptions.some((a) => a.scopeCut && draft.corrections[a.id] === undefined);
	return (
		<View style={s.section}>
			<Pressable
				accessibilityRole="button"
				accessibilityLabel={`Assumptions, ${plan.assumptions.length}`}
				accessibilityState={{ expanded: open }}
				onPress={() => setOpen((v) => !v)}
				style={s.toggle}
				hitSlop={8}
			>
				<Text style={s.heading}>{open ? "▾" : "▸"} Assumptions (accepted unless you change them) · {plan.assumptions.length}</Text>
				{!open && scopeCuts ? <Text style={s.scopeTag}>Includes a scope cut</Text> : null}
			</Pressable>
			{open ? plan.assumptions.map((a) => {
				const corrected = draft.corrections[a.id];
				const editing = correcting === a.id;
				return (
					<View key={a.id} style={[s.assumption, a.scopeCut && corrected === undefined && s.scopeCut]}>
						{a.scopeCut && corrected === undefined ? <Text style={s.scopeTag}>Drops or postpones scope</Text> : null}
						<View style={s.row}>
							<Text style={[s.text, corrected !== undefined && s.struck]}>{a.text}</Text>
							<Pressable accessibilityRole="button" accessibilityLabel={`Change: ${a.text}`} accessibilityState={{ expanded: editing }} disabled={busy} onPress={() => setCorrecting(editing ? null : a.id)} hitSlop={8}>
								<Text style={s.link}>Change</Text>
							</Pressable>
						</View>
						{editing || corrected !== undefined ? (
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
			}) : null}
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		section: { gap: space.xxs },
		toggle: { minHeight: 44, justifyContent: "center", gap: space.hair },
		heading: { color: t.textSecondary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, fontWeight: "600" },
		assumption: { gap: space.xxs },
		scopeCut: { borderLeftWidth: 3, borderLeftColor: t.amber, paddingLeft: space.sm },
		scopeTag: { color: t.amber, fontSize: type.footnote.fontSize, fontWeight: "600" },
		row: { flexDirection: "row", alignItems: "flex-start", gap: space.sm },
		text: { flex: 1, color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
		struck: { textDecorationLine: "line-through", color: t.textSecondary },
		link: { color: t.accent, fontSize: type.subheadline.fontSize },
		input: { minHeight: 44, borderRadius: radius.md, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderSubtle, paddingHorizontal: space.md, color: t.textPrimary, fontSize: type.subheadline.fontSize },
	});
