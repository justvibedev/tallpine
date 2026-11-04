import { useEffect } from "react";
import { isConfigured, type ServerConfig } from "../config";
import { type ConnectOptions } from "../connectRuntime";
import type { Endpoint } from "../endpoints";
import { activeHost, loadHosts, markHostsPairingRemoved, sameHostConnections, type Host } from "../hosts";
import { onPairingRevoked } from "../revokedPairing";
import { shouldRaceForUpgrade, UPGRADE_RACE_CHECK_MS } from "../upgradeRace";

export function usePairingLifecycle({
	reloadConfig,
	mountedRef,
	setPairedHosts,
	pairedHosts,
	selectedHostId,
	appActive,
	configResolved,
	config,
	resumedRef,
	lastReRaceAt,
}: {
	reloadConfig: (options?: ConnectOptions | undefined) => Promise<ServerConfig | null>;
	mountedRef: React.RefObject<boolean>;
	setPairedHosts: React.Dispatch<React.SetStateAction<Host[]>>;
	pairedHosts: Host[];
	selectedHostId: string | null;
	appActive: boolean;
	configResolved: boolean;
	config: ServerConfig | null;
	resumedRef: React.RefObject<boolean>;
	lastReRaceAt: React.RefObject<number>;
}) {

	useEffect(() => {
		void reloadConfig();
	}, [reloadConfig]);

	// The first 401 for a token persists that its pairing is gone, so a relaunch
	// does not try it again either, and the machine list shows it at once.
	useEffect(() => onPairingRevoked((token) => {
		void markHostsPairingRemoved(token)
			.then(() => loadHosts())
			.then((hosts) => {
				if (mountedRef.current) setPairedHosts((previous) => sameHostConnections(previous, hosts) ? previous : hosts);
			})
			.catch(() => {});
	}), []);
	const selectedPairingRemoved = pairedHosts.some((host) => host.id === selectedHostId && !!host.pairingRemoved);

	// An offline selected host still needs to reconnect when its network returns.
	// Without this, a failed initial endpoint race leaves it closed until a tap.
	useEffect(() => {
		// A removed pairing does not come back by retrying; only a scan fixes it.
		if (!appActive || !configResolved || selectedHostId === null || config || selectedPairingRemoved) return;
		const timer = setInterval(() => void reloadConfig(), 15_000);
		return () => clearInterval(timer);
	}, [appActive, configResolved, selectedHostId, config, selectedPairingRemoved, reloadConfig]);

	// Nothing re-picks a path while the current one answers, so once the app
	// fell to the tunnel it stayed there even after Wi-Fi came back — observed
	// on device, holding a Cloudflare connection with a working LAN unused.
	// This is the only thing that moves the app back up the preference order.
	useEffect(() => {
		if (!config || !isConfigured(config) || !appActive) return;
		let stopped = false;
		const check = async () => {
			if (stopped) return;
			const resumed = resumedRef.current;
			resumedRef.current = false;
			let known: Endpoint[] = [];
			try {
				known = (await activeHost())?.endpoints ?? [];
			} catch {
				return; // Storage unavailable: leave the working connection alone.
			}
			if (stopped) return;
			if (
				shouldRaceForUpgrade({
					currentKind: config.endpointKind,
					known,
					lastRaceAt: lastReRaceAt.current,
					now: Date.now(),
					resumed,
				})
			) {
				// Racing is safe even when nothing better answers: reloadConfig
				// keeps the previous config object when the endpoint is unchanged,
				// so the streams keyed on it are not torn down for nothing.
				void reloadConfig();
			}
		};
		void check();
		const id = setInterval(check, UPGRADE_RACE_CHECK_MS);
		return () => {
			stopped = true;
			clearInterval(id);
		};
	}, [config, appActive, reloadConfig]);

}
