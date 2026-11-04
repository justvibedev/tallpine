import { ChevronDown, ChevronUp, Search, X } from "lucide-react";
import type { BrowserPanelViewState } from "./browser-types";

/** Find-in-page bar. */
export function BrowserFindBar({ v }: { v: BrowserPanelViewState }) {
	const {
		closeFind,
		findComposingRef,
		findInputRef,
		findOpen,
		findQuery,
		handleFindKeyDown,
		runFind,
		setFindQuery,
		t,
		findState,
	} = v;
	return (
		<>
			{findOpen ? (
				<div className="browser-panel__find-row" role="search" data-testid="browser-find-row">
					<Search aria-hidden="true" className="size-3.5 shrink-0 text-passive" />
					<input
						ref={findInputRef}
						className="browser-panel__find-input"
						value={findQuery}
						placeholder={t("browser.find.placeholder")}
						aria-label={t("browser.find.placeholder")}
						onChange={(event) => {
							const query = event.currentTarget.value;
							setFindQuery(query);
							if (!findComposingRef.current) runFind(query);
						}}
						onCompositionStart={() => {
							findComposingRef.current = true;
						}}
						onCompositionEnd={(event) => {
							findComposingRef.current = false;
							const query = event.currentTarget.value;
							setFindQuery(query);
							runFind(query);
						}}
						onKeyDown={handleFindKeyDown}
					/>
					<span className="browser-panel__find-status" aria-live="polite" aria-atomic="true">
						{findQuery
							? findState.matches > 0
								? t("browser.find.matchCount", {
									active: findState.activeMatchOrdinal,
									total: findState.matches,
								})
								: findState.finalUpdate
									? t("browser.find.noResults")
									: ""
							: ""}
					</span>
					<button
						type="button"
						className="browser-panel__find-button"
						disabled={!findQuery}
						aria-label={t("browser.find.previous")}
						onClick={() => runFind(findQuery, false, false)}
					>
						<ChevronUp aria-hidden="true" />
					</button>
					<button
						type="button"
						className="browser-panel__find-button"
						disabled={!findQuery}
						aria-label={t("browser.find.next")}
						onClick={() => runFind(findQuery, true, false)}
					>
						<ChevronDown aria-hidden="true" />
					</button>
					<button
						type="button"
						className="browser-panel__find-button"
						aria-label={t("browser.find.close")}
						onClick={closeFind}
					>
						<X aria-hidden="true" />
					</button>
				</div>
			) : null}
		</>
	);
}
