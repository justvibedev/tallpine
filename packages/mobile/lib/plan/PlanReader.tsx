import { useState } from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { ChatMarkdown } from "../chat/ChatMarkdown";
import type { Theme } from "../theme";
import { useThemedStyles } from "../ThemeProvider";
import { space, type } from "../tokens";
import type { PlanView } from "./planCardModel";

/** Read-only view of a submitted plan: decisions first, then the PR split, the full plan on tap. */
export function PlanReader({ plan }: { plan: PlanView }) {
	const s = useThemedStyles(makeStyles);
	const [full, setFull] = useState(false);
	return (
		<View style={s.body}>
			{plan.decisionsMarkdown ? (
				<View style={s.section}>
					<Text style={s.heading}>Decisions</Text>
					<ChatMarkdown text={plan.decisionsMarkdown} />
				</View>
			) : null}
			{plan.prSplit.length > 0 ? (
				<View style={s.section}>
					<Text style={s.heading}>Pull requests</Text>
					{plan.prSplit.map((pr, index) => (
						<Text key={`${index}-${pr.title}`} style={s.text}>
							{index + 1}. <Text style={s.strong}>{pr.title}</Text>{pr.summary ? ` — ${pr.summary}` : ""}
						</Text>
					))}
				</View>
			) : null}
			{plan.planMarkdown ? (
				<View style={s.section}>
					<Pressable accessibilityRole="button" accessibilityState={{ expanded: full }} onPress={() => setFull((v) => !v)}>
						<Text style={s.link}>{full ? "Hide full plan" : "Show full plan"}</Text>
					</Pressable>
					{full ? <ChatMarkdown text={plan.planMarkdown} /> : null}
				</View>
			) : null}
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		body: { gap: space.md },
		section: { gap: space.xxs },
		heading: { color: t.textSecondary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, fontWeight: "600" },
		text: { color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
		strong: { fontWeight: "600" },
		link: { color: t.accent, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
	});
