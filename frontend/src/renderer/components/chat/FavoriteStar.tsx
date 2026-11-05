import { Star } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";

/** The star at the end of a model row. It shows on hover, and stays once starred. */
export function FavoriteStar({ starred, onToggle }: { starred: boolean; onToggle: () => void }) {
	const { t } = useTranslation();
	return (
		<span
			role="button"
			tabIndex={-1}
			aria-label={starred ? t("agentSwitch.removeFavourite") : t("agentSwitch.addFavourite")}
			title={starred ? t("agentSwitch.removeFavourite") : t("agentSwitch.addFavourite")}
			// Starring must not also pick the model.
			onPointerDown={(event) => event.stopPropagation()}
			onPointerUp={(event) => event.stopPropagation()}
			onClick={(event) => { event.preventDefault(); event.stopPropagation(); onToggle(); }}
			className={cn(
				"ml-auto flex size-5 shrink-0 items-center justify-center rounded hover:bg-interactive-active",
				starred ? "text-warning" : "text-muted-foreground opacity-0 group-hover/model:opacity-100 group-data-highlighted/model:opacity-100",
			)}
		>
			<Star aria-hidden="true" className={cn("size-3.5", starred ? "fill-current" : undefined)} />
		</span>
	);
}
