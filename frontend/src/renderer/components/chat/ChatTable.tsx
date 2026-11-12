/** A chat table: scrolls sideways inside its own box and offers Copy Markdown / Copy CSV on hover. */

import { useRef, type ComponentPropsWithoutRef } from "react";
import { tableCells, toCsv, toMarkdown } from "../../lib/table-copy";
import { CopyButton } from "./CopyButton";

export function ChatTable({ children }: ComponentPropsWithoutRef<"table">) {
	const ref = useRef<HTMLTableElement>(null);
	const cells = () => (ref.current ? tableCells(ref.current) : []);
	return (
		<div className="group/table relative my-2.5">
			<div className="overflow-x-auto rounded-lg border border-border">
				<table ref={ref} className="w-full border-collapse text-xs">
					{children}
				</table>
			</div>
			<div className="absolute right-1 top-1 flex gap-0.5 rounded-md bg-surface/90 opacity-0 transition-opacity focus-within:opacity-100 group-hover/table:opacity-100">
				<CopyButton text={() => toMarkdown(cells())} label="Copy table as Markdown" caption="Markdown" />
				<CopyButton text={() => toCsv(cells())} label="Copy table as CSV" caption="CSV" />
			</div>
		</div>
	);
}
