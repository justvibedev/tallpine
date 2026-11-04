import type { MessageKey } from "../../i18n/messages";
import { useTranslation } from "react-i18next";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";

const NONE = "__none__";
const SCOPES = ["local", "pr", "pr-merge"] as const;

/** Publishing-scope override for this item; empty means the run decides. */
export function BacklogScopeSelect({ value, onChange }: { value: string; onChange: (scope: string) => void }) {
	const { t } = useTranslation();
	return (
		<div className="flex items-center justify-between gap-3">
			<span id="backlog-scope-label" className="text-xs font-medium text-muted-foreground">{t("backlog.detail.scope")}</span>
			<Select value={value === "" ? NONE : value} onValueChange={(next) => onChange(next === NONE ? "" : next)}>
				<SelectTrigger size="sm" aria-labelledby="backlog-scope-label" className="min-w-36">
					<SelectValue />
				</SelectTrigger>
				<SelectContent>
					<SelectItem value={NONE}>{t("backlog.scope.none")}</SelectItem>
					{SCOPES.map((scope) => <SelectItem key={scope} value={scope}>{t(`backlog.scope.${scope}` as MessageKey)}</SelectItem>)}
				</SelectContent>
			</Select>
		</div>
	);
}
