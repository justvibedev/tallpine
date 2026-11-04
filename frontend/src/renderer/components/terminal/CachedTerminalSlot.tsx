/** Slot that shows a cached terminal inside a pane. */
import { useContext, useLayoutEffect, useRef } from "react";
import { TerminalCacheDescriptor, TerminalPaneProps, TerminalCacheContext } from "./terminal-pane-types";

export function CachedTerminalSlot({
	descriptor,
	props,
}: {
	descriptor: TerminalCacheDescriptor;
	props: TerminalPaneProps;
}) {
	const cache = useContext(TerminalCacheContext);
	const slotRef = useRef<HTMLDivElement | null>(null);

	useLayoutEffect(() => {
		const slot = slotRef.current;
		if (!cache || !slot) return;
		cache.activate(descriptor, props, slot);
		return () => cache.deactivate(descriptor.cacheKey, slot);
	}, [cache, descriptor.cacheKey, descriptor.handleId, descriptor.kind, descriptor.ownerKey, descriptor.sessionId]);

	useLayoutEffect(() => {
		cache?.update(descriptor.cacheKey, props);
	}, [cache, descriptor.cacheKey, props]);

	return <div className="h-full min-h-0 w-full" data-testid="session-terminal-slot" ref={slotRef} />;
}
