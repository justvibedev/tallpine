/** The identity check shown to people: the full certificate fingerprint,
 * upper-case, in groups of four. One format everywhere (Settings → Connections
 * and the add-computer confirmation) so the two can be compared by eye. */
export function formatSecurityCode(pin: string): string {
	return pin.replace(/[^0-9a-f]/gi, "").toUpperCase().replace(/(.{4})(?=.)/g, "$1 ");
}
