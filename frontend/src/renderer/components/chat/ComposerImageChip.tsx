import { createContext, useContext } from "react";
import { Image as ImageIcon } from "lucide-react";
import { useTranslation } from "react-i18next";

/** What an inline image chip shows for a staged path; the path itself is the wire text. */
export type ComposerImage = { path: string; name: string; src?: string };

export const ComposerImages = createContext<ComposerImage[]>([]);

/** An empty path is a reservation whose image is still being staged. */
export function ComposerImageChip({ path }: { path: string }) {
	const { t } = useTranslation();
	const images = useContext(ComposerImages);
	const index = images.findIndex((candidate) => candidate.path === path);
	const image = images[index];
	if (!path) {
		return (
			<span
				data-composer-token="image-pending"
				contentEditable={false}
				className="mx-0.5 inline-flex items-center gap-1 rounded-md border border-dashed border-border-strong px-1.5 py-0.5 align-middle text-[0.9em] leading-none text-muted-foreground select-none"
			>
				<ImageIcon aria-hidden="true" className="size-3 shrink-0" />
				…
			</span>
		);
	}
	return (
		<span
			data-composer-token="image"
			data-value={path}
			contentEditable={false}
			title={image?.name}
			className="mx-0.5 inline-flex max-w-48 items-center gap-1 rounded-md border border-border-strong bg-interactive-hover py-0.5 pl-0.5 pr-1.5 align-middle text-[0.9em] leading-none text-foreground select-none"
		>
			{image?.src ? (
				<img src={image.src} alt="" className="size-4 shrink-0 rounded-sm object-cover" />
			) : (
				<ImageIcon aria-hidden="true" className="size-3 shrink-0" />
			)}
			{/* Numbered like the sent message will be; the file name stays in the tooltip. */}
			<span className="truncate">
				{image ? t("chat.image.numbered", { index: index + 1 }) : t("chat.image.untitled")}
			</span>
		</span>
	);
}
