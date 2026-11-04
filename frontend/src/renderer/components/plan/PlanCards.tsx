import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { listProjectPlans, projectPlansKey } from "../../lib/plan-api";
import { PlanCard } from "./PlanCard";
import { planPhase } from "./planDockModel";

/**
 * The project's plans that wait on someone, one card each, above the composer
 * of the orchestrator chat. Renders nothing while there are none or the planner
 * switch is off. The backlog event stream refetches them (keys sit under the
 * project's backlog key).
 */
export function PlanCards({ projectId }: { projectId: string }) {
	const query = useQuery({ queryKey: projectPlansKey(projectId), queryFn: () => listProjectPlans(projectId), retry: false });
	const plans = query.data ?? [];
	// One card open at a time: the owner's explicit pick, else the first card that needs them.
	// `answered` only bridges the gap until the refetch moves a just-answered plan on.
	const [picked, setPicked] = useState<string | false | null>(null); // false: the owner collapsed the open card
	const [answered, setAnswered] = useState<ReadonlySet<string>>(new Set());
	const needsOwner = (p: (typeof plans)[number]) => {
		const phase = planPhase(p.plan);
		return (phase === "answering" && !answered.has(p.plan.id)) || phase === "review" || phase === "failed";
	};
	const pickedLive = typeof picked === "string" && plans.some((p) => p.plan.id === picked);
	const openId = picked === false ? null : pickedLive ? picked : plans.find(needsOwner)?.plan.id ?? null; // answered cards fold to their header
	if (plans.length === 0) return null;
	return (
		<div data-testid="plan-cards" className="flex max-h-[min(55vh,36rem)] min-h-0 shrink-0 flex-col gap-2 overflow-y-auto overscroll-contain">
			{plans.map((p) => (
				<PlanCard
					key={p.plan.id}
					projectId={projectId}
					itemRef={p.itemRef}
					itemTitle={p.itemTitle}
					plan={p.plan}
					collapsed={openId !== p.plan.id}
					onToggle={() => setPicked(openId === p.plan.id ? false : p.plan.id)}
					onAnswered={() => { setPicked(null); setAnswered((s) => new Set(s).add(p.plan.id)); }}
				/>
			))}
		</div>
	);
}
