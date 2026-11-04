/* Split out of ConnectMobileContent.tsx (B298, P55). */

import { ArrowUpRight } from "lucide-react";
import { type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { tallpineBridge } from "../../../lib/bridge";
import { Tooltip, TooltipContent, TooltipTrigger } from "../../ui/tooltip";
import { QrCanvas } from "../QrCanvas";
import { QR_CODE_SIZE, STORE_LINKS, STORE_LINK_CLASS, STORE_QR_SIZE } from "./store-links";

/**
 * The pairing content row: details on the left, the code on the right.
 *
 * The code's offset from the card's top edge equals its offset from the right
 * edge (the row inset, 16px). The card already pads its block edges by
 * --space-1, so the row's own top padding is the inset minus that. Bottom
 * mirrors it: 16px to the divider above Keep awake, or to the card edge when
 * this row is the card's last child.
 */
export function PairingLayout({ panel, children }: { panel: ReactNode; children: ReactNode }) {
	return (
		<div
			className="settings-content-row flex flex-col gap-6 pt-[calc(var(--settings-row-inset)-var(--space-1))] not-last:pb-(--settings-row-inset) last:pb-[calc(var(--settings-row-inset)-var(--space-1))] sm:flex-row"
			data-testid="mobile-pairing"
		>
			<div className="flex min-w-0 flex-1 flex-col">{children}</div>
			{panel}
		</div>
	);
}

/** One instruction and where to get the app. Nothing here changes once paired. */
export function ScanIntro() {
	const { t } = useTranslation();
	return (
		<>
			<p className="text-sm leading-5 text-settings-label">{t("mobile.scanHint")}</p>
			{STORE_LINKS.length > 0 && <p className="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-control leading-5 text-settings-muted">
				<span>{t("mobile.getApp")}</span>
				{STORE_LINKS.map(({ key, Icon, url, labelKey, ariaKey, testId }) => (
					<Tooltip key={key}>
						<TooltipTrigger asChild>
							<button
								type="button"
								className={STORE_LINK_CLASS}
								aria-label={t(ariaKey)}
								onClick={() => void tallpineBridge.app.openExternal(url)}
							>
								<Icon className="size-3.5 shrink-0" />
								{t(labelKey)}
								<ArrowUpRight className="size-3" aria-hidden="true" />
							</button>
						</TooltipTrigger>
						<TooltipContent side="bottom" className="p-2" data-testid={testId}>
							<div className="rounded-md bg-(--color-bg-settings-input) p-2">
								<QrCanvas value={url} size={STORE_QR_SIZE} cache className="block" />
							</div>
						</TooltipContent>
					</Tooltip>
				))}
			</p>}
		</>
	);
}

/**
 * The fixed 240px square every pairing state shares: a blurred decoy code
 * (dark on white, so Android can scan the real one that replaces it) with the
 * state's content layered over it.
 */
export function QrPlaceholder({ value, children }: { value: string; children?: ReactNode }) {
	return (
		<div className="relative size-60 shrink-0 self-start overflow-hidden rounded-md bg-white">
			<div className="absolute inset-0 opacity-60 blur-[6px]" aria-hidden="true">
				<QrCanvas value={value} size={QR_CODE_SIZE} cache className="block" />
			</div>
			{children}
		</div>
	);
}
