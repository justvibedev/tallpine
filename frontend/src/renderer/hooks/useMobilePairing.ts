/* Split out of ConnectMobileContent.tsx (B298, P55). */
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { withPairingCodeRefresh } from "../components/settings/pairingCodeRefresh";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { tallpineBridge } from "../lib/bridge";
import { MobileStatus, fetchMobileStatus, rememberKeepAwake } from "../lib/mobile-api";
import { mobileStatusQueryKey } from "../lib/mobile-status-key";
import { mobileStatusRefetchInterval } from "../lib/pairing-readiness";
import { useStayAwake } from "./useStayAwake";

/**
 * The pairing page's data: the polled mobile status, its optimistic enabled
 * flag and every mutation. ConnectMobileContent only renders and wires them.
 */
export function useMobilePairing(active: boolean) {
	const queryClient = useQueryClient();
	const [optimisticEnabled, setOptimisticEnabled] = useState<boolean | null>(null);

	// The name the phone pre-fills for this computer (macOS Computer Name);
	// the endpoint host stays as the fallback while it loads or when unavailable.
	const computerNameQuery = useQuery({
		queryKey: ["mobile", "computer-name"],
		queryFn: () => tallpineBridge.remotes.computerName(),
		staleTime: Infinity,
		retry: false,
	});

	const query = useQuery({
		queryKey: mobileStatusQueryKey,
		queryFn: fetchMobileStatus,
		enabled: active,
		// Keep refreshing through every enabled-but-not-advertisable startup
		// state — see mobileStatusRefetchInterval.
		refetchInterval: (q) => withPairingCodeRefresh(mobileStatusRefetchInterval(q.state.data), q.state.data?.enabled),
	});

	const reportedOpen = useRef(false);
	const initialEnabled = query.data?.enabled;
	useEffect(() => {
		if (!active) {
			reportedOpen.current = false;
			setOptimisticEnabled(null);
			return;
		}
		if (initialEnabled === undefined || reportedOpen.current) return;
		reportedOpen.current = true;
	}, [active, initialEnabled]);

	const keepAwakeStatus = query.data?.keepAwake;
	const hasStatus = query.data !== undefined;
	useEffect(() => {
		if (hasStatus) rememberKeepAwake(keepAwakeStatus);
	}, [hasStatus, keepAwakeStatus]);

	const invalidate = () => {
		void queryClient.invalidateQueries({ queryKey: mobileStatusQueryKey });
	};

	const serverEnabled = query.data?.enabled;
	useEffect(() => {
		if (optimisticEnabled !== null && serverEnabled === optimisticEnabled) {
			setOptimisticEnabled(null);
		}
	}, [optimisticEnabled, serverEnabled]);

	const enable = useMutation({
		mutationFn: async () => {
			const { data, error } = await apiClient.POST("/api/v1/mobile/enable");
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
		onSuccess: invalidate,
		onError: () => setOptimisticEnabled(null),
	});

	const startRemoteAccess = useMutation({
		mutationFn: async () => {
			const { data, error } = await apiClient.POST("/api/v1/mobile/remote-access");
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
		onSuccess: invalidate,
	});

	const regenerate = useMutation({
		mutationFn: async () => {
			const { data, error } = await apiClient.POST("/api/v1/mobile/regenerate");
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
		onSuccess: invalidate,
	});

	const disable = useMutation({
		mutationFn: async () => {
			const { error } = await apiClient.POST("/api/v1/mobile/disable");
			if (error) throw new Error(apiErrorMessage(error));
		},
		// Shutdown can finish before returning an error. Refresh the actual
		// server state while retaining the mutation error for the user.
		onSettled: invalidate,
		onError: () => setOptimisticEnabled(null),
	});

	const setSecure = useMutation({
		mutationFn: async (secureEnabled: boolean) => {
			const { data, error } = await apiClient.POST("/api/v1/mobile/secure-pairing", { body: { enabled: secureEnabled } });
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
		onSuccess: invalidate,
	});

	const setFeature = useMutation({
		mutationFn: async (featureEnabled: boolean) => {
			const { data, error } = await apiClient.POST("/api/v1/mobile/feature", { body: { enabled: featureEnabled } });
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
		onSettled: invalidate,
	});

	const setPublicUrl = useMutation({
		mutationFn: async (url: string) => {
			const { data, error } = await apiClient.POST("/api/v1/mobile/public-url", { body: { url } });
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
		onSuccess: invalidate,
	});

	const setQuickTunnel = useMutation({
		mutationFn: async (quickTunnel: boolean) => {
			const { data, error } = await apiClient.POST("/api/v1/mobile/quick-tunnel", { body: { enabled: quickTunnel } });
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
		onSuccess: invalidate,
	});

	// Same switch as the sidebar Stay Awake button, so the two cannot disagree.
	const stayAwake = useStayAwake(active);

	const setRequireDevicePairing = useMutation({
		mutationFn: async (requireDevicePairing: boolean) => {
			const { data, error } = await apiClient.POST("/api/v1/mobile/device-pairing", { body: { enabled: requireDevicePairing } });
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
		onMutate: async (requireDevicePairing) => {
			await queryClient.cancelQueries({ queryKey: mobileStatusQueryKey });
			const previous = queryClient.getQueryData<MobileStatus>(mobileStatusQueryKey);
			if (previous) queryClient.setQueryData<MobileStatus>(mobileStatusQueryKey, { ...previous, requireDevicePairing });
			return { previous };
		},
		onError: (_error, _enabled, context) => {
			if (context?.previous) queryClient.setQueryData(mobileStatusQueryKey, context.previous);
		},
		onSettled: invalidate,
	});

	const setBrowserRemoteAccess = useMutation({
		mutationFn: async (browserRemoteAccess: boolean) => {
			const { data, error } = await apiClient.POST("/api/v1/mobile/browser-control", { body: { enabled: browserRemoteAccess } });
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
		onMutate: async (browserRemoteAccess) => {
			await queryClient.cancelQueries({ queryKey: mobileStatusQueryKey });
			const previous = queryClient.getQueryData<MobileStatus>(mobileStatusQueryKey);
			if (previous) queryClient.setQueryData<MobileStatus>(mobileStatusQueryKey, { ...previous, browserRemoteAccess });
			return { previous };
		},
		onError: (_error, _enabled, context) => {
			if (context?.previous) queryClient.setQueryData(mobileStatusQueryKey, context.previous);
		},
		onSettled: invalidate,
	});

	// HTTPS turns itself on wherever Tailscale exists — there is no switch for
	// it. iOS refuses cleartext to a 100.x address, so a Tailscale pairing
	// without it would work on Android and fail on iPhone with nothing on
	// either side to say why.
	//
	// secureAttempted keeps it to one attempt per panel session: a tailnet with
	// no certificates fails every time, and retrying on each status poll would
	// hammer the daemon. secureReasonText surfaces the failure; reopening the
	// panel is the retry.
	const secureAttempted = useRef(false);
	useEffect(() => {
		if (!active) {
			secureAttempted.current = false;
			return;
		}
		// No tailnet address on this machine means nothing for TLS to serve.
		if (!query.data?.tailscaleHost) return;
		const secure = query.data?.securePairing;
		if (!secure || secure.enabled || !secure.available) return;
		if (secureAttempted.current || setSecure.isPending) return;
		secureAttempted.current = true;
		setSecure.mutate(true);
	}, [active, query.data?.tailscaleHost, query.data?.securePairing, setSecure]);

	return {
		query,
		computerNameQuery,
		optimisticEnabled,
		setOptimisticEnabled,
		enable,
		startRemoteAccess,
		regenerate,
		disable,
		setSecure,
		setFeature,
		setPublicUrl,
		setQuickTunnel,
		setRequireDevicePairing,
		setBrowserRemoteAccess,
		stayAwake,
	};
}
