/** Copy for the orchestrator setup placeholder while a project is provisioning. */

export function orchestratorSetupPlaceholder(provisionSteps: readonly { id?: string; status: string }[] | undefined): string {
	const currentSetupStep = provisionSteps?.find((step) => step.status === "running")?.id;
	return currentSetupStep === "fetch" ? "Getting the latest code"
		: currentSetupStep === "worktree" ? "Preparing your workspace"
		: currentSetupStep === "setup" ? "Running your project setup"
		: currentSetupStep === "agent" ? "Starting your orchestrator"
		: provisionSteps?.length && provisionSteps.every((step) => step.status === "done")
			? "Connecting to your orchestrator"
			: "Getting your project ready";
}
