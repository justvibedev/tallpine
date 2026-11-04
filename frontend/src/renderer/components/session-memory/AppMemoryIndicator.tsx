import { SessionMemoryPanel } from "../SessionMemoryPanel";
/** Machine memory bar, app memory indicator and machine section. */
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { formatMemory, useAppMemory } from "../../hooks/useSessionMemory";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { usePressureFacts, stateDot, stateText } from "./session-memory-facts";

/**
 * Archive-bar light: a dot, the state word, Tallpine's size and the fix. Colour
 * means something needs doing; grey means nothing does. The tooltip holds
 * the machine figures, the window behind it everything else.
 */
export function AppMemoryIndicator() {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const memory = useAppMemory();
	const { state } = usePressureFacts();
	const app = memory.data?.app;
	const system = memory.data?.system;
	if (memory.isError || !app || app.rssBytes === 0) {
		return null;
	}
	const word = state ? t(`shell.memoryState.${state}`) : t("shell.memoryState.unknown");
	const detail = system
		? t("shell.memoryBarDetail", {
			free: formatMemory(system.availableBytes),
			total: formatMemory(system.totalBytes),
			used: formatMemory(app.rssBytes),
			pressure: system.pressureRaw.toFixed(1),
		})
		: t("shell.memoryAppUsageNoTotal", { used: formatMemory(app.rssBytes) });
	return (
		<>
			<Tooltip>
				<TooltipTrigger asChild>
					<button
						aria-label={`${word} · ${detail}`}
						className="inline-flex items-center gap-2 font-mono text-2xs tabular-nums text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:underline"
						data-memory-state={state ?? "unknown"}
						data-testid="app-memory-indicator"
						onClick={() => setOpen(true)}
						type="button"
					>
						<span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full", state ? stateDot[state] : "bg-passive")} />
						<span className={state ? stateText[state] : undefined}>{formatMemory(app.rssBytes)}</span>
					</button>
				</TooltipTrigger>
				<TooltipContent side="top">{detail}</TooltipContent>
			</Tooltip>
			{open ? <SessionMemoryPanel onOpenChange={setOpen} open /> : null}
		</>
	);
}
