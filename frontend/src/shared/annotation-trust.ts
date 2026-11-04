/**
 * The annotation overlay shares its DOM with the page. Page script can dispatch
 * synthetic clicks and keys at `document` to drive the overlay's listeners, so
 * only events the browser itself produced from real input are honored.
 */
export function isUserInputEvent(event: Event): boolean {
	return event.isTrusted;
}
