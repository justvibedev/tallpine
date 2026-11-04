import { useCallback, useEffect, useRef, useState } from "react";
import { AppState, View } from "react-native";
import { startPlanPolling } from "./planPolling";
import { userFacingError } from "../connectionError";
import type { ServerConfig } from "../config";
import { space } from "../tokens";
import { acceptPlanDefaults, answerPlan, approvePlan, listProjectPlans } from "./planApi";
import type { PlanAnswersRequest, PlanView, ProjectPlan } from "./planCardModel";
import { PlanCard } from "./PlanCard";

const REFRESH_MS = 8000;

/** Loads a project's waiting plans and keeps them fresh while mounted. */
export function useProjectPlans(cfg: ServerConfig | null | undefined, projectId: string | undefined) {
	// Plans are tagged with the project (and config) they were fetched for, so a
	// switch never shows the old project's plans and a late response is dropped.
	const [loaded, setLoaded] = useState<{ cfg: unknown; projectId: string; plans: ProjectPlan[] }>();
	const current = useRef({ cfg, projectId });
	current.current = { cfg, projectId };
	const refresh = useCallback(async () => {
		if (!cfg || !projectId) return;
		try {
			const plans = await listProjectPlans(cfg, projectId);
			if (current.current.cfg !== cfg || current.current.projectId !== projectId) return;
			setLoaded({ cfg, projectId, plans });
		} catch { /* keep the last list; the next tick retries */ }
	}, [cfg, projectId]);
	useEffect(() => {
		return startPlanPolling(() => void refresh(), AppState, REFRESH_MS);
	}, [refresh]);
	const plans = cfg && projectId && loaded?.cfg === cfg && loaded.projectId === projectId ? loaded.plans : EMPTY;
	return { plans, refresh };
}

const EMPTY: ProjectPlan[] = [];

/** One live plan card: runs the owner's action, shows its error, refreshes after. */
export function LivePlanCard({ cfg, projectId, itemRef, itemTitle, plan, onChanged }: {
	cfg: ServerConfig; projectId: string; itemRef: string; itemTitle: string; plan: PlanView; onChanged: () => void;
}) {
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string>();
	const run = async (action: () => Promise<PlanView>) => {
		setBusy(true);
		setError(undefined);
		try { await action(); } catch (e) { setError(userFacingError(e)); } finally { setBusy(false); onChanged(); }
	};
	return (
		<PlanCard
			itemRef={itemRef}
			itemTitle={itemTitle}
			plan={plan}
			busy={busy}
			error={error}
			onAnswer={(request: PlanAnswersRequest) => void run(() => answerPlan(cfg, projectId, itemRef, request))}
			onDefaults={() => void run(() => acceptPlanDefaults(cfg, projectId, itemRef))}
			onApprove={() => void run(() => approvePlan(cfg, projectId, itemRef))}
		/>
	);
}

/** The plans that wait on the owner, one card each, for the orchestrator chat. Nothing while there are none. */
export function PlanCards({ cfg, projectId }: { cfg: ServerConfig | null | undefined; projectId: string | undefined }) {
	const { plans, refresh } = useProjectPlans(cfg, projectId);
	if (!cfg || !projectId || plans.length === 0) return null;
	return (
		<View style={{ gap: space.sm, paddingHorizontal: space.md, paddingBottom: space.sm }}>
			{plans.map((p) => (
				<LivePlanCard key={p.plan.id} cfg={cfg} projectId={projectId} itemRef={p.itemRef} itemTitle={p.itemTitle} plan={p.plan} onChanged={() => void refresh()} />
			))}
		</View>
	);
}
