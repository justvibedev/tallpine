import { useCallback, useEffect, useRef } from "react";
import { sameContent } from "../../lib/stable-list";


/**
 * A callback whose identity survives its caller re-rendering.
 *
 * `useConversationCommands` returns fresh arrows every render and the preview
 * harness passes literals, so without this every memo boundary below would be
 * invalidated by the one prop that never meaningfully changes.
 */
export function useStableCallback<Args extends unknown[], Result>(
	fn: ((...args: Args) => Result) | undefined,
): (...args: Args) => Result | undefined {
	const latest = useRef(fn);
	useEffect(() => {
		latest.current = fn;
	});
	// Only ever called from an event handler, which runs after the commit that
	// updated the ref — there is no render-phase caller to read a stale closure.
	return useCallback((...args: Args) => latest.current?.(...args), []);
}

/** Retain an unchanged JSON-shaped value across snapshot refreshes. */
export function useStableValue<T>(value: T): T {
	const previous = useRef(value);
	if (!sameContent(previous.current, value)) previous.current = value;
	return previous.current;
}
