import { useTranslation } from "react-i18next";
import { PRODUCT_NAME } from "../../shared/brand";
import { TallpineMark } from "./TallpineMark";
import { useSystemRequirementsGate } from "../hooks/useSystemRequirementsGate";
import { InstallDependencyDialog } from "./InstallDependencyDialog";

export function DaemonStartupLoader() {
	const { t } = useTranslation();
	const {
		query: requirementsQuery,
		requirements,
		requirementsBlocked,
	} = useSystemRequirementsGate();

	return (
		<div
			aria-busy="true"
			aria-label={t("startup.aria", { brand: PRODUCT_NAME })}
			className="tallpine-startup-screen flex h-full w-full items-center justify-center bg-background text-foreground"
			data-testid="daemon-startup-loader"
			role="status"
		>
			<div className="grid h-28 w-32 -translate-y-[3vh] place-items-center" aria-hidden="true">
				<TallpineMark className="tallpine-startup-logo size-24" />
			</div>
			{requirementsBlocked ? (
				<InstallDependencyDialog requirements={requirements} onRefetchRequirements={() => requirementsQuery.refetch()} />
			) : null}
		</div>
	);
}
