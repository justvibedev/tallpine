import React from "react";
import { useTranslation } from "react-i18next";

type BoundaryProps = { children: React.ReactNode; fallback: string };

class Boundary extends React.Component<BoundaryProps, { hasError: boolean }> {
	state = { hasError: false };

	static getDerivedStateFromError() {
		return { hasError: true };
	}

	render() {
		if (this.state.hasError) {
			return <p className="px-4 py-3 text-sm text-muted-foreground">{this.props.fallback}</p>;
		}
		return this.props.children;
	}
}

/** Keeps a render error inside one settings section instead of blanking the window. */
export function SectionErrorBoundary({ children }: { children: React.ReactNode }) {
	const { t } = useTranslation();
	return <Boundary fallback={t("appError.body")}>{children}</Boundary>;
}
