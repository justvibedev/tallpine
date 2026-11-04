/** Copy report control. */
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Check, Copy } from "lucide-react";
import { cn } from "@/lib/utils";
import { tallpineBridge } from "../../lib/bridge";

/**
 * Copy something from a row: the PID, or a whole session report. A tick for
 * a moment says it worked; a failure leaves the icon alone rather than
 * claiming success.
 */
export function CopyControl({
	children,
	className,
	copiedLabel,
	label,
	testId,
	value,
}: {
	children?: ReactNode;
	className?: string;
	copiedLabel: string;
	label: string;
	testId: string;
	value: () => string;
}) {
	const [copied, setCopied] = useState(false);
	const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
	useEffect(() => () => {
		if (timer.current !== null) clearTimeout(timer.current);
	}, []);
	const copy = async () => {
		try {
			await tallpineBridge.clipboard.writeText(value());
		} catch {
			return;
		}
		setCopied(true);
		if (timer.current !== null) clearTimeout(timer.current);
		timer.current = setTimeout(() => setCopied(false), 1_500);
	};
	return (
		<button
			aria-label={copied ? copiedLabel : label}
			className={cn(
				"group inline-flex shrink-0 items-center gap-1 rounded-sm text-settings-muted transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
				className,
			)}
			data-testid={testId}
			onClick={(event) => {
				event.stopPropagation();
				void copy();
			}}
			type="button"
		>
			{children}
			{copied ? (
				<Check aria-hidden="true" className="size-icon-2xs text-success" />
			) : (
				<Copy
					aria-hidden="true"
					className={cn(
						"size-icon-2xs transition-opacity",
						// Beside a PID the icon is a hint that fades in; on its own it
						// is the whole control and must stay visible.
						children ? "opacity-0 group-hover:opacity-100 group-focus-visible:opacity-100" : undefined,
					)}
				/>
			)}
		</button>
	);
}
