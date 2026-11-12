/** Serialises a rendered table's cell text for the clipboard as Markdown or CSV. */

export type TableCells = string[][];

/** Visible text only: screen-reader-only and aria-hidden nodes are dropped. */
function cellText(cell: HTMLTableCellElement): string {
	const clone = cell.cloneNode(true) as HTMLTableCellElement;
	clone.querySelectorAll(".sr-only, [aria-hidden='true']").forEach((n) => n.remove());
	return (clone.textContent ?? "").replace(/\s+/g, " ").trim();
}

/** Cell text of every row, whitespace collapsed; reads the DOM so inline markup is flattened. */
export function tableCells(table: HTMLTableElement): TableCells {
	return Array.from(table.rows).map((row) => Array.from(row.cells).map(cellText));
}

/** RFC 4180: quote fields holding a comma, quote, or line break; double inner quotes. */
export function toCsv(rows: TableCells): string {
	const field = (v: string) => (/[",\r\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v);
	return rows.map((r) => r.map(field).join(",")).join("\r\n");
}

/** GFM table: first row is the header; pipes and backslashes are escaped. */
export function toMarkdown(rows: TableCells): string {
	if (rows.length === 0) return "";
	const width = Math.max(...rows.map((r) => r.length));
	const line = (r: string[]) =>
		`| ${Array.from({ length: width }, (_, i) => (r[i] ?? "").replace(/\\/g, "\\\\").replace(/\|/g, "\\|")).join(" | ")} |`;
	const sep = `| ${Array.from({ length: width }, () => "---").join(" | ")} |`;
	return [line(rows[0]), sep, ...rows.slice(1).map(line)].join("\n");
}
