import { Plus, X } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "../ui/button";
import { Input } from "../ui/input";

type BacklogFieldsProps = {
	fields: Readonly<Record<string, string>>;
	onSet: (key: string, value: string) => Promise<unknown>;
	onClear: (key: string) => Promise<unknown>;
};

const KEY_PATTERN = /^[a-z0-9][a-z0-9._-]{0,39}$/;

/** The orchestrator's key to value fields as a small table; the user can add, edit and clear them. */
export function BacklogFields({ fields, onSet, onClear }: BacklogFieldsProps) {
	const { t } = useTranslation();
	const [key, setKey] = useState("");
	const [value, setValue] = useState("");
	const [error, setError] = useState<string | null>(null);
	const entries = Object.entries(fields).sort(([a], [b]) => a.localeCompare(b));

	const run = async (action: () => Promise<unknown>) => {
		setError(null);
		try {
			await action();
		} catch (failure) {
			setError(t("backlog.saveError", { message: failure instanceof Error ? failure.message : String(failure) }));
		}
	};
	const add = () => {
		if (!KEY_PATTERN.test(key.trim()) || value.trim() === "") return;
		void run(async () => {
			await onSet(key.trim(), value.trim());
			setKey("");
			setValue("");
		});
	};

	return (
		<div>
			<span className="text-xs font-medium text-muted-foreground">{t("backlog.detail.fields")}</span>
			{entries.length === 0 ? <p className="text-sm text-muted-foreground">{t("backlog.fields.none")}</p> : null}
			{entries.length > 0 ? (
				<table className="mt-1 w-full text-sm">
					<tbody className="divide-y divide-border/40">
						{entries.map(([name, current]) => (
							<tr key={name}>
								<th scope="row" className="w-1/3 py-1 pr-3 text-left font-normal text-muted-foreground">{name}</th>
								<td className="py-1">
									<Input
										aria-label={t("backlog.fields.valueOf", { key: name })}
										defaultValue={current}
										className="h-7 text-xs"
										onBlur={(event) => {
											const next = event.target.value.trim();
											if (next === current) return;
											if (next === "") void run(() => onClear(name));
											else void run(() => onSet(name, next));
										}}
									/>
								</td>
								<td className="w-8 py-1 text-right">
									<Button type="button" variant="ghost" size="icon-sm" aria-label={t("backlog.fields.remove", { key: name })} onClick={() => void run(() => onClear(name))}>
										<X aria-hidden="true" className="size-3.5" />
									</Button>
								</td>
							</tr>
						))}
					</tbody>
				</table>
			) : null}
			<div className="mt-2 flex items-center gap-2">
				<Input aria-label={t("backlog.fields.key")} placeholder={t("backlog.fields.key")} value={key} onChange={(event) => setKey(event.target.value)} className="h-7 w-32 text-xs" />
				<Input aria-label={t("backlog.fields.value")} placeholder={t("backlog.fields.value")} value={value} onChange={(event) => setValue(event.target.value)} onKeyDown={(event) => event.key === "Enter" && add()} className="h-7 min-w-0 flex-1 text-xs" />
				<Button type="button" variant="ghost" size="icon-sm" aria-label={t("backlog.fields.add")} onClick={add}>
					<Plus aria-hidden="true" className="size-3.5" />
				</Button>
			</div>
			{error ? <p role="alert" className="mt-1 text-xs text-destructive">{error}</p> : null}
		</div>
	);
}
