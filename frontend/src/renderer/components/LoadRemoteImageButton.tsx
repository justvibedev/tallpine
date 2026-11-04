import { ImageDown } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/utils";

/** Click-to-load placeholder for a remote image in agent output: nothing leaves the machine until it is pressed. */
export function LoadRemoteImageButton({ url, alt, loading, onLoad, className }: { url: string; alt?: string; loading: boolean; onLoad: () => void; className?: string }) {
	const { t } = useTranslation();
	let host = url;
	try {
		host = new URL(url).host;
	} catch {
		// keep the raw string
	}
	return (
		<button
			type="button"
			disabled={loading}
			onClick={(event) => {
				event.preventDefault();
				event.stopPropagation();
				onLoad();
			}}
			title={alt || undefined}
			className={cn("inline-flex items-center gap-1.5 rounded-md border border-border px-2 py-1 text-[0.9em] text-muted-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60", className)}
		>
			<ImageDown aria-hidden="true" className="size-3.5 shrink-0" />
			<span>{t("chat.image.loadRemote", { host })}</span>
		</button>
	);
}
