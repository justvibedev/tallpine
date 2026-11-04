/** Session link provider for the xterm terminal. */
import { Terminal, type ILink, type ILinkProvider } from "@xterm/xterm";
import { findSessionLinks } from "../../lib/session-links";

export function mapStringOffsetToBuffer(
	term: Terminal,
	lineIndex: number,
	columnIndex: number,
	stringOffset: number,
): [number, number] | undefined {
	const buffer = term.buffer.active;
	const cell = buffer.getNullCell();
	let startColumn = columnIndex;
	while (stringOffset > 0) {
		const line = buffer.getLine(lineIndex);
		if (!line) return undefined;
		for (let column = startColumn; column < line.length; column += 1) {
			line.getCell(column, cell);
			if (cell.getWidth() > 0) stringOffset -= cell.getChars().length || 1;
			if (stringOffset < 0) return [lineIndex, column];
		}
		lineIndex += 1;
		startColumn = 0;
	}
	return [lineIndex, startColumn];
}

export function sessionLinkProvider(
	term: Terminal,
	activate: (event: MouseEvent, uri: string) => void,
): ILinkProvider {
	return {
		provideLinks(lineNumber, callback) {
			const buffer = term.buffer.active;
			let firstLine = lineNumber - 1;
			while (firstLine > 0 && buffer.getLine(firstLine)?.isWrapped) firstLine -= 1;
			const lines = [];
			for (let line = firstLine; line < buffer.length; line += 1) {
				if (line > firstLine && !buffer.getLine(line)?.isWrapped) break;
				lines.push(buffer.getLine(line)?.translateToString(false) ?? "");
			}
			const text = lines.join("");
			const links: ILink[] = findSessionLinks(text).flatMap((match) => {
				const start = mapStringOffsetToBuffer(term, firstLine, 0, match.start);
				if (!start) return [];
				const end = mapStringOffsetToBuffer(term, start[0], start[1], match.text.length);
				if (!end) return [];
				const [startLine, startColumn] = start;
				let [endLine, endColumn] = end;
				if (endColumn === 0 && endLine > startLine) {
					endLine -= 1;
					endColumn = term.cols;
				}
				if (lineNumber - 1 < startLine || lineNumber - 1 > endLine) return [];
				return [
					{
						text: match.text,
						range: {
							start: { x: startColumn + 1, y: startLine + 1 },
							end: { x: endColumn, y: endLine + 1 },
						},
						activate: (event) => activate(event, match.text),
					},
				];
			});
			callback(links.length > 0 ? links : undefined);
		},
	};
}
