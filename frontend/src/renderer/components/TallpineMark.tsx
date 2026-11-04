/** The Tallpine pine mark (frontend/assets/tallpine/brand/mark-pine.svg), drawn in currentColor so it follows light and dark themes. */
export function TallpineMark({ className }: { className: string }) {
	return (
		<svg className={className} viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor">
			<path d="M12 3V22M7.5 8 12 3l4.5 5M5 13.5 12 8l7 5.5M2.8 19 12 13.5l9.2 5.5" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" />
			<g fill="currentColor" stroke="none">
				<circle cx="12" cy="3" r="2.1" /><circle cx="7.5" cy="8" r="2.1" /><circle cx="16.5" cy="8" r="2.1" />
				<circle cx="5" cy="13.5" r="2.1" /><circle cx="19" cy="13.5" r="2.1" />
				<circle cx="2.8" cy="19" r="2.1" /><circle cx="21.2" cy="19" r="2.1" />
			</g>
		</svg>
	);
}
