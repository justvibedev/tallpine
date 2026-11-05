import { useTranslation } from "react-i18next";
import { PRODUCT_NAME } from "../../shared/brand";
import { TallpineMark } from "./TallpineMark";
import { useSystemRequirementsGate } from "../hooks/useSystemRequirementsGate";
import { InstallDependencyDialog } from "./InstallDependencyDialog";
import { DaemonFailureBanner } from "./DaemonFailureBanner";
import type { DaemonStatus } from "../../shared/daemon-status";

// `status` explains a start that is waiting or was refused (B568) under the mark.
export function DaemonStartupLoader({ status }: { status?: DaemonStatus } = {}) {
	const { t } = useTranslation();
	const {
		query: requirementsQuery,
		requirements,
		requirementsBlocked,
	} = useSystemRequirementsGate();

	return (
		<div
			className="tallpine-startup-screen flex h-full w-full flex-col items-center justify-center gap-6 bg-background text-foreground"
			data-testid="daemon-startup-loader"
		>
			{/* The banner is a sibling, not a child, so its alert is not muted by this busy status. */}
			<div aria-busy="true" aria-label={t("startup.aria", { brand: PRODUCT_NAME })} role="status">
				<div className="grid h-28 w-32 -translate-y-[3vh] place-items-center" aria-hidden="true">
					<TallpineMark className="tallpine-startup-logo size-24" />
				</div>
			</div>
			{status ? <DaemonFailureBanner status={status} placement="inline" /> : null}
			{requirementsBlocked ? (
				<InstallDependencyDialog requirements={requirements} onRefetchRequirements={() => requirementsQuery.refetch()} />
			) : null}
		</div>
	);
}
