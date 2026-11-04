import { useTranslation } from "react-i18next";
import { type ReactNode } from "react";
import { inspectorEmptyClass } from "@tallpine/product-ui";
import { Button } from "../ui/button";
import { cn } from "../../lib/utils";

export function FilesView({ filesView, onOpenFiles }: { filesView?: ReactNode; onOpenFiles?: () => void }) {
	const { t } = useTranslation();
	if (filesView) {
		return (
			<div className="h-full min-h-0" role="tabpanel">
				{filesView}
			</div>
		);
	}
	return (
		<div role="tabpanel">
			<div className={cn(inspectorEmptyClass, "flex flex-col items-center gap-2 px-5 py-10 text-center")}>
				<p className="text-md-sm text-muted-foreground">{t("inspector.filesUnavailable")}</p>
				<Button disabled={!onOpenFiles} onClick={() => onOpenFiles?.()} size="sm" type="button" variant="outline">
					{t("inspector.openFiles")}
				</Button>
			</div>
		</div>
	);
}
