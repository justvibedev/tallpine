import { render } from "@testing-library/react";
import { vi } from "vitest";
import { AgentModelCombobox } from "./AgentModelCombobox";

export function renderCombobox(
	models: React.ComponentProps<typeof AgentModelCombobox>["models"],
	overrides: Partial<React.ComponentProps<typeof AgentModelCombobox>> = {},
) {
	const onChange = vi.fn();
	const onCustom = vi.fn();
	const view = render(
		<AgentModelCombobox
			aria-label="Worker model"
			value=""
			models={models}
			allowCustom
			onChange={onChange}
			onCustom={onCustom}
			{...overrides}
		/>,
	);
	return { ...view, onChange, onCustom };
}
