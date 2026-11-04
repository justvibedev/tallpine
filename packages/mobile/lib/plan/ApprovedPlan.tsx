import { useEffect, useState } from "react";
import { StyleSheet, Text, View } from "react-native";
import type { ServerConfig } from "../config";
import type { Theme } from "../theme";
import { useThemedStyles } from "../ThemeProvider";
import { space, type } from "../tokens";
import { Button } from "../ui";
import { getItemPlan } from "./planApi";
import type { PlanView } from "./planCardModel";
import { PlanReader } from "./PlanReader";

/**
 * An item's approved plan on the item screen. The list endpoint carries no plan
 * summary (only single-item reads do), so this fetches the item's plan itself
 * and renders nothing unless it is approved. Re-plan stays on desktop in v1.
 */
export function ApprovedPlan({ cfg, projectId, itemRef }: { cfg: ServerConfig; projectId: string; itemRef: string }) {
	const s = useThemedStyles(makeStyles);
	const [plan, setPlan] = useState<PlanView | null>(null);
	const [open, setOpen] = useState(false);
	useEffect(() => {
		let cancelled = false;
		getItemPlan(cfg, projectId, itemRef).then((p) => { if (!cancelled) setPlan(p); }, () => {});
		return () => { cancelled = true; };
	}, [cfg, projectId, itemRef]);
	if (!plan || plan.status !== "approved") return null;
	return (
		<View style={s.box}>
			<Text style={s.muted}>Plan approved{plan.stale ? " — files it names changed since; re-plan on desktop" : plan.itemChanged ? " — the item was edited after planning" : ""}</Text>
			{open ? <PlanReader plan={plan} /> : <Button title="Read plan" icon="file-text" variant="ghost" onPress={() => setOpen(true)} />}
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		box: { gap: space.sm },
		muted: { color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	});
