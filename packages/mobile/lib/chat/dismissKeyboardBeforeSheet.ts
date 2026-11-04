import { Keyboard } from "react-native";

export async function dismissKeyboardBeforeSheet(keyboardVisible: boolean): Promise<void> {
	Keyboard.dismiss();
	if (!keyboardVisible) return;
	await new Promise<void>((resolve) => {
		let settled = false;
		let subscription: ReturnType<typeof Keyboard.addListener> | undefined;
		const finish = () => {
			if (settled) return;
			settled = true;
			subscription?.remove();
			clearTimeout(timeout);
			resolve();
		};
		const timeout = setTimeout(finish, 320);
		subscription = Keyboard.addListener("keyboardDidHide", finish);
	});
}
