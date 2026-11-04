import type { MessageKey } from "../../i18n/messages";
import { Plus } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { BacklogLinkKind } from "../../lib/backlog-api";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";

const KINDS: readonly BacklogLinkKind[] = ["session", "pr", "issue", "artifact", "memory", "item", "blocked_by"];

type BacklogLinkAddProps = { onAdd: (kind: BacklogLinkKind, target: string) => Promise<unknown> };

/** Kind, target, add. Item and blocked-by targets are refs such as B3. */
export function BacklogLinkAdd({ onAdd }: BacklogLinkAddProps) {
	const { t } = useTranslation();
	const [kind, setKind] = useState<BacklogLinkKind>("pr");
	const [target, setTarget] = useState("");
	const [error, setError] = useState<string | null>(null);
	const submit = async () => {
		const value = target.trim();
		if (!value) return;
		setError(null);
		try {
			await onAdd(kind, value);
			setTarget("");
		} catch (failure) {
			setError(t("backlog.saveError", { message: failure instanceof Error ? failure.message : String(failure) }));
		}
	};
	return (
		<div>
			<div className="flex items-center gap-2">
				<Select value={kind} onValueChange={(value) => setKind(value as BacklogLinkKind)}>
					<SelectTrigger size="sm" aria-label={t("backlog.link.kind")} className="w-32">
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						{KINDS.map((value) => <SelectItem key={value} value={value}>{t(`backlog.link.kind.${value}` as MessageKey)}</SelectItem>)}
					</SelectContent>
				</Select>
				<Input
					aria-label={t("backlog.link.target")}
					placeholder={t("backlog.link.targetPlaceholder")}
					value={target}
					onChange={(event) => setTarget(event.target.value)}
					onKeyDown={(event) => event.key === "Enter" && void submit()}
					className="h-7 min-w-0 flex-1 text-xs"
				/>
				<Button type="button" variant="ghost" size="icon-sm" aria-label={t("backlog.link.add")} onClick={() => void submit()}>
					<Plus aria-hidden="true" className="size-3.5" />
				</Button>
			</div>
			{error ? <p role="alert" className="mt-1 text-xs text-destructive">{error}</p> : null}
		</div>
	);
}
