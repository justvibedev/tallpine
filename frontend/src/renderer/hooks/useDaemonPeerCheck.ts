import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { silentFailuresQueryKey, useSilentFailures } from "./useSilentFailures";

/** The owner's off switch for the daemon peer check (B631). */
export const PEER_CHECK_KEY = "app.daemon.peerCheck";

/**
 * The daemon peer check: whether this build can check callers at all, the
 * effective mode, and the owner's switch. Only the signed desktop app may change
 * the switch; the daemon refuses anyone else with SETTING_DESKTOP_ONLY.
 */
export function useDaemonPeerCheck() {
	const queryClient = useQueryClient();
	const { data } = useSilentFailures();
	const check = data?.peerCheck;
	const mutation = useMutation({
		mutationFn: async (on: boolean) => {
			const { error } = await apiClient.PATCH("/api/v1/settings/values", { body: { set: { [PEER_CHECK_KEY]: on ? "default" : "off" } } });
			if (error) throw error;
		},
		// The diagnostics report the mode the daemon now runs with; read it back rather than assume.
		onSettled: () => queryClient.invalidateQueries({ queryKey: silentFailuresQueryKey }),
	});
	return {
		check,
		on: check ? check.mode !== "off" : false,
		setOn: (on: boolean) => mutation.mutate(on),
		saving: mutation.isPending,
		error: mutation.error ? apiErrorMessage(mutation.error) : undefined,
	};
}
