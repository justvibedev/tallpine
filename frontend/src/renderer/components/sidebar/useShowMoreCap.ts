import { useEffect, useMemo, useState } from "react";

/** Caps a sidebar list at `limit` behind Show more/less. The cap lifts on its
 *  own when the active item sits past it, until the user collapses it again. */
export function useShowMoreCap<T extends { id: string }>(
	items: T[],
	limit: number,
	activeId: string | undefined,
	isCollapsed = false,
) {
	const [showAll, setShowAll] = useState(false);
	const [showAllDismissed, setShowAllDismissed] = useState(false);
	const activeBeyondLimit = useMemo(() => {
		if (showAll || items.length <= limit || !activeId) return false;
		return items.findIndex((item) => item.id === activeId) >= limit;
	}, [activeId, items, limit, showAll]);
	useEffect(() => setShowAllDismissed(false), [activeId]);
	useEffect(() => {
		if (activeBeyondLimit && !showAllDismissed) setShowAll(true);
	}, [activeBeyondLimit, showAllDismissed]);
	const listed = useMemo(
		() => (isCollapsed || showAll || items.length <= limit ? items : items.slice(0, limit)),
		[isCollapsed, items, limit, showAll],
	);
	const toggleShowAll = () => {
		const next = !showAll;
		setShowAll(next);
		setShowAllDismissed(!next);
	};
	return { listed, hiddenCount: Math.max(0, items.length - limit), showAll, toggleShowAll };
}
