/** A plain multi-line field for project settings rows. */
export function ProjectTextArea({ id, label, value, rows, placeholder, onChange }: { id: string; label: string; value: string; rows: number; placeholder?: string; onChange: (value: string) => void }) {
	return <textarea id={id} aria-label={label} rows={rows} value={value} placeholder={placeholder} spellCheck={false} onChange={(event) => onChange(event.target.value)}
		className="w-full min-w-0 resize-y rounded-md border border-settings-border bg-transparent px-2 py-1.5 font-mono text-sm text-settings-label placeholder:text-settings-muted" />;
}
