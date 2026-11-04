import { useCallback, useEffect, useState } from "react";
import type { UpdatePreferences, UpdateSettings, UpdateStatus } from "../../main/update-settings";
import { tallpineBridge } from "../lib/bridge";

/** Live status of the desktop app updater (GitHub Releases). */
export function useAppUpdateStatus(): UpdateStatus | null {
	const [status, setStatus] = useState<UpdateStatus | null>(null);
	useEffect(() => {
		let live = true;
		const off = tallpineBridge.appUpdates.onStatus((next) => setStatus(next));
		void tallpineBridge.appUpdates.getStatus().then((next) => {
			if (live) setStatus((current) => current ?? next);
		}, () => undefined);
		return () => {
			live = false;
			off();
		};
	}, []);
	return status;
}

/** Auto-update preferences, including whether managed config locks them. */
export function useAppUpdatePreferences() {
	const [preferences, setPreferences] = useState<UpdatePreferences | null>(null);
	const [error, setError] = useState<string | null>(null);
	const [saving, setSaving] = useState(false);
	useEffect(() => {
		let live = true;
		void tallpineBridge.appUpdates.getPreferences().then((next) => {
			if (live) setPreferences(next);
		}, () => undefined);
		return () => {
			live = false;
		};
	}, []);
	const update = useCallback(async (settings: UpdateSettings) => {
		setSaving(true);
		setError(null);
		try {
			await tallpineBridge.appUpdates.setSettings(settings);
			setPreferences(await tallpineBridge.appUpdates.getPreferences());
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : String(cause));
		} finally {
			setSaving(false);
		}
	}, []);
	return { preferences, update, saving, error };
}
