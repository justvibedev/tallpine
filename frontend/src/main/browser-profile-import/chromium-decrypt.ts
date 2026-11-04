/* Split out of browser-profile-import.ts (B297, P53). */

import { spawn } from "node:child_process";
import { createDecipheriv, createHash, pbkdf2Sync } from "node:crypto";
import path from "node:path";
import { InternalSource, LOCAL_STATE_MAX_BYTES, execFileAsync, isRecord, readSmallJSON, throwIfImportAborted } from "./common";

export class ChromiumCookieDecryptor {
	private constructor(
		private readonly platform: NodeJS.Platform,
		private readonly key: Buffer | null,
	) {}

	static async create(source: InternalSource, platform: NodeJS.Platform, signal: AbortSignal): Promise<ChromiumCookieDecryptor> {
		if (source.descriptor.family !== "chromium") return new ChromiumCookieDecryptor(platform, null);
		if (platform === "win32") {
			try {
				const localState = await readSmallJSON(path.join(source.root, "Local State"), LOCAL_STATE_MAX_BYTES);
				if (!isRecord(localState) || !isRecord(localState.os_crypt) || typeof localState.os_crypt.encrypted_key !== "string") {
					return new ChromiumCookieDecryptor(platform, null);
				}
				const wrapped = Buffer.from(localState.os_crypt.encrypted_key, "base64");
				if (!wrapped.subarray(0, 5).equals(Buffer.from("DPAPI"))) return new ChromiumCookieDecryptor(platform, null);
				return new ChromiumCookieDecryptor(platform, await windowsDPAPIUnprotect(wrapped.subarray(5)));
			} catch {
				return new ChromiumCookieDecryptor(platform, null);
			}
		}
		if (platform === "darwin") {
			return new ChromiumCookieDecryptor(platform, await readMacChromiumPassword(source.descriptor.chromiumKeychainNames ?? [], signal));
		}
		return new ChromiumCookieDecryptor(platform, null);
	}

	decrypt(encrypted: Buffer, host: string): string | null {
		if (encrypted.length === 0 || !this.key) return null;
		if (this.platform === "win32") return decryptWindowsChromiumCookie(encrypted, this.key, host);
		if (this.platform === "darwin") return decryptMacChromiumCookie(encrypted, this.key, host);
		return null;
	}
}

export async function readMacChromiumPassword(
	names: string[],
	signal: AbortSignal,
	run: typeof execFileAsync = execFileAsync,
): Promise<Buffer> {
	for (const name of names) {
		try {
			const { stdout } = await run("security", ["find-generic-password", "-w", "-s", `${name} Safe Storage`], {
				timeout: 120_000, maxBuffer: 16 * 1024, signal,
			});
			const password = stdout.trim();
			if (password) return Buffer.from(password);
			throw new Error("Empty Safe Storage key");
		} catch (error) {
			throwIfImportAborted(signal);
			// security maps errSecItemNotFound (-25300) to exit status 44.
			// Denial, cancellation and timeout must not trigger another prompt.
			if ((error as { code?: unknown }).code === 44) continue;
			if ((error as { killed?: boolean }).killed) {
				throw new Error("Timed out waiting for macOS Safe Storage access. Retry and respond to the macOS prompt, or turn off cookies to import history only.");
			}
			throw new Error("macOS did not grant access to the browser's Safe Storage key. Retry and allow access, or turn off cookies to import history only.");
		}
	}
	throw new Error("The browser's Safe Storage key was not found. Turn off cookies to import history only.");
}

export function decryptWindowsChromiumCookie(encrypted: Buffer, key: Buffer, host: string): string | null {
	if (encrypted.subarray(0, 3).toString() !== "v10" && encrypted.subarray(0, 3).toString() !== "v11") return null;
	if (encrypted.length < 3 + 12 + 16) return null;
	try {
		const nonce = encrypted.subarray(3, 15);
		const payload = encrypted.subarray(15);
		const ciphertext = payload.subarray(0, -16);
		const authTag = payload.subarray(-16);
		const decipher = createDecipheriv("aes-256-gcm", key, nonce, { authTagLength: 16 });
		decipher.setAuthTag(authTag);
		return stripChromiumCookieHostDigest(
			Buffer.concat([decipher.update(ciphertext), decipher.final()]),
			host,
		).toString("utf8");
	} catch {
		return null;
	}
}

export function decryptMacChromiumCookie(encrypted: Buffer, password: Buffer, host: string): string | null {
	const prefix = encrypted.subarray(0, 3).toString();
	if (prefix !== "v10" && prefix !== "v11") return null;
	try {
		const key = pbkdf2Sync(password, "saltysalt", 1_003, 16, "sha1");
		const decipher = createDecipheriv("aes-128-cbc", key, Buffer.alloc(16, 0x20));
		const plaintext = Buffer.concat([decipher.update(encrypted.subarray(3)), decipher.final()]);
		return stripChromiumCookieHostDigest(plaintext, host).toString("utf8");
	} catch {
		return null;
	}
}

export function stripChromiumCookieHostDigest(plaintext: Buffer, host: string): Buffer {
	// Chromium cookie DB v24 prefixes encrypted values with SHA-256(host_key)
	// and verifies/removes it after platform decryption. Older databases do not,
	// so only strip an exact digest match.
	const hostDigest = createHash("sha256").update(host).digest();
	return plaintext.subarray(0, hostDigest.length).equals(hostDigest)
		? plaintext.subarray(hostDigest.length)
		: plaintext;
}

export async function windowsDPAPIUnprotect(input: Buffer): Promise<Buffer | null> {
	const script = [
		"Add-Type -AssemblyName System.Security",
		"$raw = [Console]::In.ReadToEnd()",
		"$bytes = [Convert]::FromBase64String($raw)",
		"$plain = [Security.Cryptography.ProtectedData]::Unprotect($bytes, $null, [Security.Cryptography.DataProtectionScope]::CurrentUser)",
		"[Console]::Out.Write([Convert]::ToBase64String($plain))",
	].join("; ");
	return new Promise((resolve) => {
		const child = spawn("powershell.exe", ["-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script], {
			stdio: ["pipe", "pipe", "ignore"],
			windowsHide: true,
		});
		let stdout = "";
		const timer = setTimeout(() => child.kill(), 10_000);
		child.stdout.setEncoding("utf8");
		child.stdout.on("data", (chunk: string) => {
			if (stdout.length < 32 * 1024) stdout += chunk;
		});
		child.once("error", () => {
			clearTimeout(timer);
			resolve(null);
		});
		child.once("close", (code) => {
			clearTimeout(timer);
			if (code !== 0) return resolve(null);
			try {
				resolve(Buffer.from(stdout.trim(), "base64"));
			} catch {
				resolve(null);
			}
		});
		child.stdin.end(input.toString("base64"));
	});
}
