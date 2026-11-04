/* Split out of ConnectMobileContent.tsx (B298, P55). */

import { Check, Coffee, Globe } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { KeepAwakeStatus } from "../../../lib/mobile-api";
import { Button } from "../../ui/button";
import { Input } from "../../ui/input";
import { Switch } from "../../ui/switch";
import { SettingsRow } from "../SettingsRow";

export type ReachableState = "on" | "notSetUp" | "starting" | "off" | "failed";

/** One connection the phone can use, with its state. */
export function ReachableLine({ label, state, testId }: { label: string; state: ReachableState; testId: string }) {
	const { t } = useTranslation();
	const text = t(state === "on" ? "mobile.reachable.on" : `mobile.reachable.${state}`);
	return (
		<li className="flex items-center gap-2" data-testid={testId} data-state={state}>
			<span className="text-settings-label">{label}</span>
			<span className={state === "failed" ? "text-warning" : "text-settings-muted"}>
				{state === "on" ? <Check className="inline size-3.5" aria-hidden="true" /> : null}
				<span className={state === "on" ? "sr-only" : ""}>{text}</span>
			</span>
		</li>
	);
}

/** The reachable base URL pairing codes advertise: a Tailscale MagicDNS/100.x
 *  address, or the user's own reverse proxy. The daemon validates it (and
 *  refuses cleartext http to a public host); its error is shown as-is. */
export function PublicUrlRow({
	saved,
	state,
	pending,
	onSave,
}: {
	saved: string;
	state?: string;
	pending: boolean;
	onSave: (url: string) => void;
}) {
	const { t } = useTranslation();
	const [draft, setDraft] = useState(saved);
	useEffect(() => setDraft(saved), [saved]);
	const dirty = draft.trim() !== saved;
	return (
		<SettingsRow
			icon={Globe}
			label={t("mobile.publicUrl.label", "Reachable address")}
			description={t(
				"mobile.publicUrl.help",
				"Optional. An https URL the phone can reach this computer at from outside your network, such as https://my-mac.tailnet.ts.net (Tailscale Serve) or your own HTTPS reverse proxy. Pairing codes advertise it alongside the local address. Phones never connect over plain http.",
			)}
			data-testid="mobile-public-url"
		>
			{saved !== "" && state === "unreachable" && (
				<p className="text-xs text-destructive" data-testid="mobile-public-url-unreachable">
					{t(
						"mobile.publicUrl.unreachable",
						"Nothing is answering at this address, so it is not offered to phones. Check that Tailscale Serve or your proxy is running.",
					)}
				</p>
			)}
			{saved !== "" && state === "ok" && (
				<p className="text-xs text-muted-foreground">{t("mobile.publicUrl.ok", "Reachable.")}</p>
			)}
			{saved === "" && (
				<p className="text-xs text-muted-foreground">{t("mobile.publicUrl.notConfigured", "Not configured.")}</p>
			)}
			<form
				className="flex w-96 max-w-full items-center gap-2"
				onSubmit={(event) => {
					event.preventDefault();
					if (dirty && !pending) onSave(draft.trim());
				}}
			>
				<Input
					aria-label={t("mobile.publicUrl.label", "Reachable address")}
					placeholder="https://tallpine.example.com"
					value={draft}
					onChange={(event) => setDraft(event.target.value)}
					spellCheck={false}
					autoComplete="off"
				/>
				<Button type="submit" size="sm" variant="ghost" disabled={!dirty || pending}>
					{draft.trim() === "" ? t("mobile.publicUrl.clear", "Clear") : t("mobile.publicUrl.save", "Save")}
				</Button>
			</form>
		</SettingsRow>
	);
}

/** Keep this Mac awake, and (when the daemon reports it) keep it awake with the
 *  lid closed. Disabled while it shows a remembered state rather than the
 *  daemon's, so it cannot be toggled on a guess; the lid row appears only for a
 *  live status, since the remembered one does not carry the lid fields. */
export function KeepAwakeRow({
	keepAwake,
	disabled = false,
	onCheckedChange,
	onLidChange,
}: {
	keepAwake: KeepAwakeStatus;
	disabled?: boolean;
	onCheckedChange?: (next: boolean) => void;
	onLidChange?: (next: boolean) => void;
}) {
	const { t } = useTranslation();
	const lidNeedsAttention =
		(keepAwake.enabled && keepAwake.lidClosed) ||
		keepAwake.lidReason === "recovery_failed" ||
		keepAwake.lidReason === "lease_failed" ||
		keepAwake.lidReason === "helper_upgrade_required";
	return (
		<>
			<SettingsRow
				data-testid="mobile-keep-awake"
				icon={Coffee}
				label={t("mobile.keepAwake.label")}
				description={
					<>
						{t("mobile.keepAwake.help")}
						{keepAwake.hasBattery && (!keepAwake.lidClosed || !keepAwake.lidSupported || !keepAwake.enabled) && (
							<span data-testid="mobile-keep-awake-laptop"> {t("mobile.keepAwake.laptopNote")}</span>
						)}
					</>
				}
			>
				<Switch
					aria-label={t("mobile.keepAwake.label")}
					checked={keepAwake.enabled}
					disabled={disabled}
					onCheckedChange={onCheckedChange}
				/>
			</SettingsRow>
			{onLidChange && (keepAwake.lidSupported || keepAwake.lidClosed) && (
				<SettingsRow
					data-testid="mobile-lid-awake"
					label={t("mobile.keepAwake.lidLabel", "Keep awake with lid closed")}
					description={
						<>
							{t(
								"mobile.keepAwake.lidHelp",
								"Works while mobile access is on, even when your phone disconnects. Uses more battery; keep the Mac on a ventilated surface. Stops below 10% battery or at critical heat.",
							)}
							{lidNeedsAttention && (
								<span className="mt-1 block" role="status">
									{keepAwake.lidActive && keepAwake.enabled
										? t("mobile.keepAwake.lidActive", "Closed-lid keep awake is armed.")
										: keepAwake.lidReason
											? t(
													`mobile.keepAwake.lidReason.${keepAwake.lidReason}`,
													"Closed-lid keep awake is unavailable. Turn the option off and on to retry.",
												)
											: t("mobile.keepAwake.lidIdle", "Starts when mobile access is on.")}
								</span>
							)}
						</>
					}
				>
					<Switch
						aria-label={t("mobile.keepAwake.lidLabel", "Keep awake with lid closed")}
						checked={keepAwake.enabled && (keepAwake.lidClosed ?? false)}
						disabled={disabled}
						onCheckedChange={onLidChange}
					/>
				</SettingsRow>
			)}
		</>
	);
}
