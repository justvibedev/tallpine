/** Boot spinner and status steps shown before a terminal attaches. */
import { useEffect, useState } from "react";

export const BOOT_SPINNER_FRAMES = ["|", "/", "-", "\\"];
export const BOOT_STATUS_STEPS = [
	"Preparing the {subject}",
	"Clearing a workspace",
	"Warming up the agent",
	"Untangling worktrees",
	"Sharpening pencils",
	"Waking the {subject}",
	"Almost there",
];

export function TerminalBootStatus({ subject, label }: { subject: string; label?: string }) {
	const [tick, setTick] = useState(0);
	useEffect(() => {
		const id = window.setInterval(() => setTick((value) => value + 1), 120);
		return () => window.clearInterval(id);
	}, []);
	const step = Math.min(Math.floor(tick / 15), BOOT_STATUS_STEPS.length - 1);
	return (
		<div className="flex items-center gap-2.5 text-terminal-dim" role="status" aria-live="polite">
			<span aria-hidden="true" className="inline-block w-[1ch] shrink-0 text-center font-mono">{BOOT_SPINNER_FRAMES[tick % BOOT_SPINNER_FRAMES.length]}</span>
			<span>{label ?? BOOT_STATUS_STEPS[step].replace("{subject}", subject)}</span>
		</div>
	);
}
