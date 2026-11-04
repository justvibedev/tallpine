import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { fetchProjectSkills, projectSkillsQueryKey } from "../lib/skills-offer";
import { useSkillsOfferStore } from "../stores/skills-offer-store";
import { useUiStore } from "../stores/ui-store";

const TOAST_MS = 12_000;

/**
 * When a project shows up in the project list after the initial load, whichever
 * flow added it (local folder, clone, workspace), raises a small toast with a
 * Review action if its instructions or skills are invisible to its agents.
 */
export function useSkillsOfferOnProjectAdd(projectIds: readonly string[] | undefined) {
	const known = useRef<Set<string> | null>(null);
	const client = useQueryClient();
	const { t } = useTranslation();
	useEffect(() => {
		if (!projectIds) return;
		if (known.current === null) {
			known.current = new Set(projectIds);
			return;
		}
		for (const id of projectIds) {
			if (known.current.has(id)) continue;
			known.current.add(id);
			void client.fetchQuery({ queryKey: projectSkillsQueryKey(id), queryFn: () => fetchProjectSkills(id), staleTime: 0 }).then((skills) => {
				if (!skills.offersEnabled || skills.offers.length === 0) return;
				useUiStore.getState().showGlobalToast(t("skillsNotice.toast"), undefined, {
					dedupeKey: `skills-offer:${id}`,
					durationMs: TOAST_MS,
					action: { label: t("skillsNotice.review"), onClick: () => useSkillsOfferStore.getState().openReview(id) },
				});
			}).catch(() => undefined);
		}
	}, [projectIds, client, t]);
}
