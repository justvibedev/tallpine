import { ChevronDown } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { OwnerAsk } from "../../lib/ask-api";
import { cn } from "../../lib/utils";
import { AskCard } from "./AskCard";
import { earlierAsk } from "./askModel";
import { useChatAsks } from "./useChatAsks";

/** Scrolls to the inline card an ask left on the turn that asked. */
export function jumpToAsk(ask: OwnerAsk) {
	document.querySelector(`[data-ask-ref="${CSS.escape(ask.ref)}"]`)?.scrollIntoView({ block: "center", behavior: "smooth" });
}

/**
 * "Waiting on you": the open asks of this chat, pinned in the composer dock
 * above the plan cards. A lane's asks show only in its chat; the main chat gets
 * a one-line pointer per lane ask. It can fold to its header, but never hides
 * while an ask is open, and scrolls inside when long.
 */
export function AskStrip({ projectId, lane, onOpenCount }: { projectId: string; lane?: string; /** Told how many open asks this chat shows, so the empty-state hero can step aside. */ onOpenCount?: (count: number) => void }) {
	const { t } = useTranslation();
	const [collapsed, setCollapsed] = useState(false);
	const { all, asks, elsewhere } = useChatAsks(projectId, lane);
	const count = asks.length;
	useEffect(() => { onOpenCount?.(count); return () => onOpenCount?.(0); }, [count, onOpenCount]);
	if (asks.length === 0 && elsewhere.length === 0) return null;
	return (
		<div data-testid="ask-strip" className="flex max-h-[min(45vh,28rem)] min-h-0 shrink-0 flex-col gap-1.5 overflow-y-auto overscroll-contain">
			{elsewhere.map((a) => (
				<p key={a.id} data-testid="ask-lane-pointer" className="px-1 text-xs text-muted-foreground">
					{t("ask.laneWaiting", { ref: a.ref, lane: a.lane ?? "" })}
				</p>
			))}
			{asks.length > 0 ? (
				<>
					<button
						type="button"
						aria-expanded={!collapsed}
						data-testid="ask-strip-header"
						onClick={() => setCollapsed((v) => !v)}
						className="flex items-center gap-1.5 px-1 text-left text-xs font-medium text-foreground"
					>
						<ChevronDown aria-hidden="true" className={cn("size-3.5 transition-transform", collapsed && "-rotate-90")} />
						{t("ask.waitingOnYou", { count: asks.length })}
					</button>
					{collapsed ? null : asks.map((a) => <AskCard key={a.id} projectId={projectId} ask={a} earlier={earlierAsk(all, a)} onJump={jumpToAsk} />)}
				</>
			) : null}
		</div>
	);
}
