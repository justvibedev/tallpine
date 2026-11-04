/* Split out of ConnectMobileContent.tsx (B298, P55). */

import { Check, ChevronRight, Cloud, Copy, Loader2, MonitorSmartphone, RotateCcw, Smartphone, TriangleAlert } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useMobilePairing } from "../../hooks/useMobilePairing";
import { readLastKeepAwake } from "../../lib/mobile-api";
import { localOnlyReason, pairingAddressLines, placeholderQrValue, qrIsReady, qrValueFor } from "../../lib/pairing-readiness";
import { formatSecurityCode } from "../../lib/security-code";
import { thisComputerOptions } from "../../lib/this-computer";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { InstallCloudflared } from "./InstallCloudflared";
import { PairingLayout, QrPlaceholder, ScanIntro } from "./mobile/PairingPanel";
import { PairingQr } from "./mobile/PairingQr";
import { KeepAwakeRow, PublicUrlRow, ReachableLine, ReachableState } from "./mobile/RemoteAccessSection";
import { ICON_ACTION_CLASS, QR_CODE_SIZE } from "./mobile/store-links";
import { reasonMessage } from "./securePairingReason";
import { SettingsRow } from "./SettingsRow";
import { Spinner } from "../ui/spinner";

export function ConnectMobileContent({ active }: { active: boolean }) {
	const { t } = useTranslation();
	const [copied, setCopied] = useState(false);
	// The code crossfades only after the user has generated or regenerated one
	// on this visit; opening the page shows whatever exists without a transition.
	const [animateQr, setAnimateQr] = useState(false);
	const copiedTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
	const [advancedOpen, setAdvancedOpen] = useState(false);

	useEffect(() => {
		return () => {
			if (copiedTimeoutRef.current) clearTimeout(copiedTimeoutRef.current);
		};
	}, []);

	const { query, computerNameQuery, optimisticEnabled, setOptimisticEnabled, enable, startRemoteAccess, regenerate, disable, setSecure, setFeature, setPublicUrl, setQuickTunnel, setRequireDevicePairing, setBrowserRemoteAccess, stayAwake } = useMobilePairing(active);

	const status = query.data;
	const enabled = optimisticEnabled ?? status?.enabled ?? false;
	// Tunnel-only hosts deliberately have no LAN address. Use the same daemon
	// endpoint list for both the offer and the address shown beside it.
	const activeEndpoint = status?.endpoints?.[0];
	const activeHost = activeEndpoint?.host ?? "";
	const activePort = activeEndpoint?.port ?? 0;
	const noEndpointAvailable =
		!activeEndpoint &&
		(status?.noPublicTunnel === true || status?.tunnel?.supported === false || Boolean(status?.tunnel?.lastError));
	// No connector on this machine at all: cloudflared is absent and nothing
	// installs it. Pairing still works on the local network, so this is a
	// caveat to state rather than a failure to block on.
	const remoteAccessUnavailable = status?.quickTunnel === true && status?.tunnel ? status.tunnel.supported === false : false;
	const localOnly = status ? localOnlyReason({ ...status, enabled }) : null;
	const viaTailscale = status?.endpoints?.some((e) => e.kind === "tailscale") ?? false;

	const busy =
		enable.isPending ||
		startRemoteAccess.isPending ||
		regenerate.isPending ||
		disable.isPending ||
		setSecure.isPending ||
		setBrowserRemoteAccess.isPending ||
		setRequireDevicePairing.isPending ||
		stayAwake.busy;

	const clearActionErrors = () => {
		enable.reset();
		startRemoteAccess.reset();
		regenerate.reset();
		disable.reset();
		setSecure.reset();
		setBrowserRemoteAccess.reset();
		setRequireDevicePairing.reset();
		setFeature.reset();
		setPublicUrl.reset();
		setQuickTunnel.reset();
	};

	const copyPassword = async () => {
		if (!status?.password) return;
		try {
			await navigator.clipboard.writeText(status.password);
			setCopied(true);
			if (copiedTimeoutRef.current) clearTimeout(copiedTimeoutRef.current);
			copiedTimeoutRef.current = setTimeout(() => setCopied(false), 1500);
		} catch {
			// Clipboard can reject (permissions / non-secure context).
		}
	};

	const startBridge = () => {
		if (busy || enabled) return;
		clearActionErrors();
		setAnimateQr(true);
		setOptimisticEnabled(true);
		enable.mutate();
	};

	const actionError =
		(enable.error instanceof Error && enable.error.message) ||
		(startRemoteAccess.error instanceof Error && startRemoteAccess.error.message) ||
		(regenerate.error instanceof Error && regenerate.error.message) ||
		(disable.error instanceof Error && disable.error.message) ||
		(setSecure.error instanceof Error && setSecure.error.message) ||
		(setBrowserRemoteAccess.error instanceof Error && setBrowserRemoteAccess.error.message) ||
		(setRequireDevicePairing.error instanceof Error && setRequireDevicePairing.error.message) ||
		(setFeature.error instanceof Error && setFeature.error.message) ||
		(setPublicUrl.error instanceof Error && setPublicUrl.error.message) ||
		(setQuickTunnel.error instanceof Error && setQuickTunnel.error.message) ||
		stayAwake.error ||
		null;

	const turnOff = () => {
		clearActionErrors();
		setOptimisticEnabled(false);
		disable.mutate();
	};

	// Each return is one or more direct children of the settings card: the
	// pairing is a content row, keep-awake a SettingsRow, divided by the card.
	// Loading keeps the pairing layout, and Keep awake in its last-known state,
	// so the card does not jump from one short row to the full block when the
	// status lands. Settings prefetches the status, so this is a cold start only.
	if (query.isLoading) {
		const lastKeepAwake = readLastKeepAwake();
		return (
			<>
				<PairingLayout panel={<QrPlaceholder value={placeholderQrValue()} />}>
					<ScanIntro />
					<p className="sr-only" role="status">
						{t("mobile.checkingStatus")}
					</p>
				</PairingLayout>
				{lastKeepAwake?.supported && <KeepAwakeRow keepAwake={lastKeepAwake} disabled />}
			</>
		);
	}
	if (query.isError) {
		return (
			<p className="settings-content-row text-xs text-error">
				{query.error instanceof Error ? query.error.message : t("mobile.loadFailed")}
			</p>
		);
	}
	if (!status) return null;

	const featureRow = (
		<SettingsRow
			icon={Smartphone}
			label={t("mobile.feature.label", "Enable mobile access")}
			description={t(
				"mobile.feature.help",
				"Lets the {{brand}} mobile app pair with this computer. Off by default: while off, {{brand}} runs no mobile listener, pairing endpoints, push notifications or tunnels.",
			)}
			data-testid="mobile-feature"
		>
			<Switch
				aria-label={t("mobile.feature.label", "Enable mobile access")}
				disabled={setFeature.isPending}
				checked={status.featureEnabled ?? false}
				onCheckedChange={(next) => {
					clearActionErrors();
					setFeature.mutate(next);
				}}
			/>
		</SettingsRow>
	);
	if (!status.featureEnabled) {
		return (
			<>
				{featureRow}
				{actionError && <p className="settings-content-row text-xs text-error">{actionError}</p>}
			</>
		);
	}

	const showRealQR =
		enabled &&
		qrIsReady({ enabled, endpoints: status?.endpoints, tunnel: status?.tunnel });
	// Computed once so the rendered v2 QR and its data attribute cannot drift.
	const qrValue = showRealQR
		? qrValueFor({
				hostId: status?.hostId ?? "",
				host: computerNameQuery.data || activeHost,
				platform: "",
				password: status?.password ?? "",
				endpoints: status?.endpoints ?? [],
			})
		: null;
	const secureReasonText = reasonMessage(status.securePairing?.reason ?? "", t);
	const unreachable = enabled && !qrValue && noEndpointAvailable;

	// One object throughout: the blurred placeholder sits at the back, the real
	// code crossfades in over it (and over the previous code on a change), and
	// the overlay says what is happening while there is no code.
	const panel = (
		<QrPlaceholder value={placeholderQrValue({ ...status, host: activeHost || status.host })}>
			<PairingQr value={qrValue} size={QR_CODE_SIZE} animate={animateQr} />
			{!enabled ? (
				<div className="absolute inset-0 flex items-center justify-center">
					<Button
						type="button"
						variant="footer-primary"
						className="rounded-md shadow-lg"
						onClick={startBridge}
						disabled={busy || enabled}
					>
						{t("mobile.generate")}
					</Button>
				</div>
			) : !qrValue && !unreachable ? (
				<div
					className="absolute inset-0 flex items-center justify-center"
					data-testid="mobile-pairing-preparing"
					role="status"
					aria-label={t("mobile.checkingStatus")}
				>
					<span className="flex h-(--size-settings-action-height) items-center rounded-(--radius-settings-action) bg-(--color-bg-secondary) px-3 text-settings-muted shadow-lg">
						<Spinner icon={Loader2}  className="size-4" />
					</span>
				</div>
			) : null}
		</QrPlaceholder>
	);

	const legacyDate = status.legacyUntil ? new Date(status.legacyUntil).toLocaleDateString(undefined, { dateStyle: "medium" }) : "";
	const addressLines = pairingAddressLines(status.endpoints, `${activeHost}:${activePort}`);
	const hasLan = status.endpoints?.some((e) => e.kind === "lan") ?? false;
	const hasTunnel = status.endpoints?.some((e) => e.kind === "tunnel") ?? false;
	// Internet: the one connection with a start-up phase and a fix.
	const internetState: ReachableState = hasTunnel
		? "on"
		: remoteAccessUnavailable
			? "notSetUp"
			: localOnly === "starting"
				? "starting"
				: localOnly === "failed"
					? "failed"
					: "off";

	return (
		<>
			<PairingLayout panel={panel}>
				{/* No connection picker: one code carries every advertised address
				    (Wi-Fi, Tailscale, internet) and the phone races them. */}
				<ScanIntro />

				{qrValue && (
					<dl className="mt-5 grid grid-cols-[auto_minmax(0,1fr)] items-center gap-x-6 gap-y-1 text-control leading-6">
						<dt className="text-settings-muted">{t("mobile.password")}</dt>
						<dd className="flex min-w-0 items-center">
							<span className="min-w-0 truncate font-mono text-settings-label">{status.password}</span>
							<span className="ml-2 flex shrink-0 items-center">
								<Tooltip>
									<TooltipTrigger asChild>
										<button
											type="button"
											aria-label={copied ? t("mobile.passwordCopied") : t("mobile.copyPassword")}
											className={ICON_ACTION_CLASS}
											onClick={() => void copyPassword()}
										>
											{copied ? <Check className="size-3.5" aria-hidden="true" /> : <Copy className="size-3.5" aria-hidden="true" />}
										</button>
									</TooltipTrigger>
									<TooltipContent side="bottom">
										{copied ? t("mobile.passwordCopied") : t("mobile.copyPassword")}
									</TooltipContent>
								</Tooltip>
								<Tooltip>
									<TooltipTrigger asChild>
										{/* Span so the tooltip still opens while the button is disabled. */}
										<span className="inline-flex">
											<button
												type="button"
												aria-label={t("mobile.regenerate")}
												className={ICON_ACTION_CLASS}
												disabled={busy}
												onClick={() => {
													clearActionErrors();
													setAnimateQr(true);
													regenerate.mutate();
												}}
											>
												{regenerate.isPending ? (
													<Spinner icon={Loader2}  className="size-3.5" />
												) : (
													<RotateCcw className="size-3.5" aria-hidden="true" />
												)}
											</button>
										</span>
									</TooltipTrigger>
									<TooltipContent side="bottom">{t("mobile.regenerate")}</TooltipContent>
								</Tooltip>
							</span>
						</dd>
					</dl>
				)}
				{qrValue && (
					<p className="mt-1 text-control leading-5 text-settings-muted" data-testid="mobile-pairing-code-hint">
						{t("mobile.pairingCodeHint")}
					</p>
				)}

				{unreachable && (
					<p className="mt-5 flex items-start gap-1.5 text-control leading-5 text-warning">
						<TriangleAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
						{t("mobile.noPairingHost", thisComputerOptions())}
					</p>
				)}

				{/* Tailscale HTTPS: shown only when it could not start. */}
				{status.tailscaleHost && secureReasonText && (
					<p data-testid="secure-pairing-reason" className="mt-4 text-control leading-5 text-warning">
						{secureReasonText}
					</p>
				)}

				{qrValue && (
					<div className="mt-5" data-testid="mobile-reachable">
						<p className="text-control leading-5 text-settings-muted">{t("mobile.reachable.title")}</p>
						<ul className="mt-1 flex flex-col text-control leading-6">
							<ReachableLine label={t("mobile.reachable.wifi")} state={hasLan ? "on" : "off"} testId="reachable-wifi" />
							<ReachableLine label={t("mobile.reachable.tailscale")} state={viaTailscale ? "on" : "notSetUp"} testId="reachable-tailscale" />
							<ReachableLine label={t("mobile.reachable.internet")} state={internetState} testId="reachable-internet" />
						</ul>
					</div>
				)}

				{qrValue && localOnly && (
					// The code refreshes itself once internet access is advertised;
					// until then it is a Wi-Fi-only code that looks exactly like a full one.
					<div className="mt-3" data-testid="mobile-local-only" role="status">
						<p className="flex items-start gap-1.5 text-xs leading-5 text-settings-muted">
							{localOnly === "starting" && <Spinner icon={Loader2}  className="mt-0.5 size-3.5 shrink-0" />}
							{localOnly === "starting"
								? viaTailscale
									? t("mobile.remoteAccessStartingTailscale")
									: t("mobile.remoteAccessStarting")
								: viaTailscale
									? t("mobile.remoteAccessFailedTailscale")
									: t("mobile.remoteAccessFailed")}
						</p>
						{localOnly === "failed" && (
							<Button
								type="button"
								variant="ghost"
								size="sm"
								className="-ml-[calc(--spacing(2.5)+1px)] mt-1 text-settings-muted hover:text-settings-label"
								disabled={busy || startRemoteAccess.isPending}
								onClick={() => {
									clearActionErrors();
									startRemoteAccess.mutate();
								}}
							>
								{t("notify.retry")}
							</Button>
						)}
					</div>
				)}

				{remoteAccessUnavailable && (
					// Stated rather than hidden: pairing still works on this Wi-Fi, so
					// this is a limit on what the code can reach, not an error.
					<div className="mt-3">
						<p className="text-xs text-settings-muted" data-testid="mobile-remote-unavailable">
							{t("mobile.remoteAccessUnavailable")}
						</p>
						{/* Deliberately not enable(): that mints a fresh pairing code, so
						    setting up internet access would invalidate the phone the user
						    had already paired. This re-checks for the connector and starts
						    it, leaving the code alone. */}
						<InstallCloudflared onInstalled={() => void startRemoteAccess.mutate()} />
					</div>
				)}
				{actionError && <p className="mt-3 text-xs text-error">{actionError}</p>}

				{/* Bottom of the details column, level with the code's bottom edge. */}
				{enabled && (
					<div className="mt-auto pt-4">
						<Button
							type="button"
							variant="ghost"
							size="sm"
							className="-ml-[calc(--spacing(2.5)+1px)] text-settings-muted hover:text-settings-label"
							disabled={busy}
							onClick={turnOff}
						>
							{t("mobile.disable", "Turn off mobile connection")}
						</Button>
					</div>
				)}
			</PairingLayout>

			{featureRow}
			{/* macOS only — the daemon reports it unsupported elsewhere. Shown
			    whether or not the bridge is on: the choice is remembered and takes
			    effect whenever the mobile connection is. */}
			{status.keepAwake?.supported && (
				<KeepAwakeRow
					keepAwake={{ ...status.keepAwake, enabled: stayAwake.on, lidClosed: stayAwake.lidOn }}
					disabled={busy || !stayAwake.supported}
					onCheckedChange={(next) => {
						clearActionErrors();
						stayAwake.set(next, true);
					}}
					onLidChange={(next) => {
						clearActionErrors();
						stayAwake.setLid(next);
					}}
				/>
			)}

			<button
				type="button"
				className="settings-content-row flex w-full items-center gap-1.5 text-left text-control text-settings-muted hover:text-settings-label focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
				aria-expanded={advancedOpen}
				data-testid="mobile-advanced-toggle"
				onClick={() => setAdvancedOpen((open) => !open)}
			>
				<ChevronRight className={`size-3.5 transition-transform ${advancedOpen ? "rotate-90" : ""}`} aria-hidden="true" />
				{t("mobile.advanced")}
			</button>
			{advancedOpen && (
				<>
					<SettingsRow label={t("mobile.address")} data-testid="mobile-pairing-address">
						<div className="min-w-0 text-right font-mono text-control text-settings-label">
							{addressLines.map((line) => (
								<div key={line} className="truncate">
									{line}
								</div>
							))}
						</div>
					</SettingsRow>
					{status.tlsPin && (
						<SettingsRow label={t("mobile.securityCode")} data-testid="mobile-tls-status">
							<span className="max-w-72 break-words text-right font-mono text-control text-settings-label" data-testid="mobile-security-code">
								{formatSecurityCode(status.tlsPin)}
							</span>
						</SettingsRow>
					)}
					<PublicUrlRow
						saved={status.publicBaseUrl ?? ""}
						state={status.publicUrlState}
						pending={setPublicUrl.isPending}
						onSave={(url) => {
							clearActionErrors();
							setPublicUrl.mutate(url);
						}}
					/>
					<SettingsRow
						icon={Cloud}
						label={t("mobile.quickTunnel.label", "Cloudflare quick tunnel")}
						description={t(
							"mobile.quickTunnel.help",
							"Off by default. Publishes this computer through a temporary trycloudflare.com address so a phone can reach it from anywhere. Cloudflare terminates TLS for this tunnel, so Cloudflare can see the traffic, including the pairing code. Prefer Tailscale or your own HTTPS proxy above.",
						)}
						data-testid="mobile-quick-tunnel"
					>
						<Switch
							aria-label={t("mobile.quickTunnel.label", "Cloudflare quick tunnel")}
							disabled={busy || setQuickTunnel.isPending}
							checked={status.quickTunnel ?? false}
							onCheckedChange={(next) => {
								clearActionErrors();
								setQuickTunnel.mutate(next);
							}}
						/>
					</SettingsRow>
					<SettingsRow
						icon={MonitorSmartphone}
						label={t("mobile.browserRemote.label", "Allow live browser on mobile")}
						description={t("mobile.browserRemote.help", "Streams browser content and allows touch control from paired phones. This is separate from the mobile connection and is off by default.")}
						data-testid="mobile-browser-remote-access"
					>
						<Switch
							aria-label={t("mobile.browserRemote.label", "Allow live browser on mobile")}
							disabled={busy}
							checked={status.browserRemoteAccess ?? false}
							onCheckedChange={(next) => {
								clearActionErrors();
								setBrowserRemoteAccess.mutate(next);
							}}
						/>
					</SettingsRow>
					{status.legacyPasswordActive && (
						<p data-testid="mobile-legacy-password-warning" className="flex items-start gap-1.5 px-4 py-2 text-control leading-5 text-warning">
							<TriangleAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
							{t("mobile.legacyPasswordWarning")}
						</p>
					)}
					<SettingsRow
						icon={Smartphone}
						label={t("mobile.requireDevicePairing.label", "Require per-device pairing (blocks older mobile apps)")}
						description={legacyDate ? t("mobile.requireDevicePairing.helpUntil", { date: legacyDate }) : t("mobile.requireDevicePairing.help")}
						data-testid="mobile-require-device-pairing"
					>
						<Switch
							aria-label={t("mobile.requireDevicePairing.label", "Require per-device pairing (blocks older mobile apps)")}
							disabled={busy}
							checked={status.requireDevicePairing ?? false}
							onCheckedChange={(next) => {
								clearActionErrors();
								setRequireDevicePairing.mutate(next);
							}}
						/>
					</SettingsRow>
				</>
			)}
		</>
	);
}
