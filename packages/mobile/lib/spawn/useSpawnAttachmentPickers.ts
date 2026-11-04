import * as DocumentPicker from "expo-document-picker";
import * as ImagePicker from "expo-image-picker";
import { File } from "expo-file-system";
import { userFacingError } from "../connectionError";
import { appendSpawnAttachments, MAX_SPAWN_ATTACHMENTS, photoPickerAttachment, readSpawnAttachments, type PickedSpawnAttachment, type SpawnAttachment } from "../spawn-attachments";

export function useSpawnAttachmentPickers({
	pickingAttachments,
	setAttachmentError,
	attachmentsRef,
	setAttachments,
}: {
	pickingAttachments: React.RefObject<boolean>;
	setAttachmentError: React.Dispatch<React.SetStateAction<string | undefined>>;
	attachmentsRef: React.RefObject<SpawnAttachment[]>;
	setAttachments: React.Dispatch<React.SetStateAction<SpawnAttachment[]>>;
}) {
	const addAttachments = async (pick: () => Promise<PickedSpawnAttachment[] | null>, fallback: string) => {
		if (pickingAttachments.current) return;
		pickingAttachments.current = true;
		setAttachmentError(undefined);
		try {
			const picked = await pick();
			if (!picked) return;
			const before = attachmentsRef.current;
			const next = await readSpawnAttachments(before, picked);
			const merged = appendSpawnAttachments(attachmentsRef.current, next.attachments.slice(before.length));
			attachmentsRef.current = merged.attachments;
			setAttachments(merged.attachments);
			setAttachmentError(next.error ?? merged.error);
		} catch (cause) {
			setAttachmentError(userFacingError(cause, fallback));
		} finally {
			pickingAttachments.current = false;
		}
	};
	const pickFiles = () => addAttachments(async () => {
		const result = await DocumentPicker.getDocumentAsync({
			multiple: true,
			copyToCacheDirectory: true,
			type: "*/*",
		});
		if (result.canceled) return null;
		return result.assets.map((asset) => {
			const file = new File(asset.uri);
			return {
				name: asset.name,
				mimeType: asset.mimeType || "application/octet-stream",
				bytes: asset.size ?? file.size,
				previewUri: asset.uri,
				readData: () => file.base64(),
			};
		});
	}, "Couldn't read that file.");
	const pickPhotos = () => addAttachments(async () => {
		const remaining = MAX_SPAWN_ATTACHMENTS - attachmentsRef.current.length;
		if (remaining <= 0) throw new Error(`You can attach up to ${MAX_SPAWN_ATTACHMENTS} files.`);
		// "compatible" asks iOS for a JPEG rather than the library's HEIC
		// original, which most agents can't open.
		const result = await ImagePicker.launchImageLibraryAsync({
			mediaTypes: ["images"],
			quality: 0.82,
			allowsMultipleSelection: true,
			selectionLimit: remaining,
			preferredAssetRepresentationMode: ImagePicker.UIImagePickerPreferredAssetRepresentationMode.Compatible,
		});
		if (result.canceled) return null;
		return result.assets.map((asset, index) => photoPickerAttachment(asset, index, new File(asset.uri)));
	}, "Couldn't open your photo library.");

	return { pickFiles, pickPhotos };
}
