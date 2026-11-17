import { cn } from "../../lib/utils";
/** A plain multi-line field for project settings rows. `autoGrow` (the rules boxes) makes it at least 10rem tall and grows it with its text up to 60% of the window height. */
export function ProjectTextArea({ id, label, value, rows, placeholder, autoGrow, onChange }: { id: string; label: string; value: string; rows: number; placeholder?: string; autoGrow?: boolean; onChange: (value: string) => void }) {
	return <textarea id={id} aria-label={label} rows={rows} value={value} placeholder={placeholder} spellCheck={false} onChange={(event) => onChange(event.target.value)}
		className={cn("w-full min-w-0 resize-y rounded-md border border-settings-border bg-transparent px-2 py-1.5 font-mono text-sm text-settings-label placeholder:text-settings-muted", autoGrow && "field-sizing-content max-h-[60vh] min-h-40")} />;
}
