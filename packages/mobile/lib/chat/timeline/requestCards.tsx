import { Feather } from "../../icons";
import * as Linking from "expo-linking";
import { useState } from "react";
import { Pressable, Switch, Text, View } from "react-native";
import { haptics } from "../../haptics";
import { useTheme, useThemedStyles } from "../../ThemeProvider";
import { actionControlWidth, requestPresentation } from "../chatPresentation";
import { ElicitationAction, ElicitationChoiceList, ElicitationTextField } from "../elicitation-native-controls";
import {
	elicitationPromptCopy,
	elicitationStepPresentation,
	groupedQuestionInputs,
	humanizeInputName,
	initialInputValue,
	inputOptions,
	missingRequiredInputs,
	safeHttpURL,
	toggleInputValue,
	validateInput,
} from "../elicitationModel";
import type { ConversationActivity, InputProperty } from "../types";
import { userFacingError } from "../../connectionError";
import { PRODUCT_NAME } from "../../brand";
import { LabelValue } from "./activityRows";
import { makeStyles } from "./styles";

/**
 * What was asked, with no way to answer it — for a request whose controls live
 * in the composer. The timeline stays the record; the composer is the surface.
 */
export function RequestEcho({ title, detail }: { title: string; detail?: string }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return <View style={styles.approvalResolved}>
		<View style={[styles.approvalDot, { backgroundColor: t.amber }]} />
		<Text style={styles.approvalResolvedLabel}>{title}</Text>
		{detail ? <Text selectable numberOfLines={1} style={styles.approvalResolvedCommand}>{detail}</Text> : null}
	</View>;
}

export function ApprovalCard({ activity, busy, onDecide, handledBelow }: { activity: ConversationActivity; busy: boolean; onDecide(requestId: string, decisionId: string): Promise<void>; handledBelow?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [submitting, setSubmitting] = useState<string>();
	const [submitError, setSubmitError] = useState<string>();
	const pending = activity.status === "pending";
	const presentation = requestPresentation("approval", pending);
	const command = activity.detail?.command ?? activity.summary;
	if (pending && handledBelow) return <RequestEcho title={presentation.title} detail={command} />;
	if (!pending) return <View style={styles.approvalResolved}>
		<Feather name={presentation.icon} size={12} color={t.textFaint} />
		<Text style={styles.approvalResolvedLabel}>{presentation.title}</Text>
		<Text selectable numberOfLines={1} style={styles.approvalResolvedCommand}>{command}</Text>
	</View>;
	return <View style={styles.approvalRequest}>
		<View style={styles.approvalStatus}><View style={styles.approvalDot} /><Text style={styles.approvalStatusText}>{presentation.title}</Text></View>
		{activity.detail?.reason ? <Text selectable style={styles.requestCopy}>{activity.detail.reason}</Text> : null}
		<View style={styles.approvalCommandSurface}>
			<Feather name="terminal" size={12} color={t.textTertiary} />
			<Text selectable style={styles.requestCommand}>{command}</Text>
		</View>
		{activity.detail?.cwd ? <LabelValue label="cwd" value={activity.detail.cwd} /> : null}
		{activity.decisions?.length ? <View style={styles.approvalActions}>{activity.decisions.map((decision, index) => {
			const label = submitting === decision.id ? "Sending…" : decision.label;
			return <ElicitationAction key={decision.id} label={label} width={actionControlWidth(label, index === 0)} primary={index === 0} disabled={busy || Boolean(submitting) || !activity.requestId} onPress={() => {
				setSubmitting(decision.id);
				setSubmitError(undefined);
				void onDecide(activity.requestId ?? "", decision.id).catch((cause) => setSubmitError(userFacingError(cause))).finally(() => setSubmitting(undefined));
			}} />;
		})}</View> : <Text style={[styles.partial, { color: t.amber }]}>The agent offered no decisions {PRODUCT_NAME} can present. Open diagnostics from the host.</Text>}
		{submitError ? <Text accessibilityRole="alert" selectable style={styles.validation}>{submitError}</Text> : null}
	</View>;
}

export function UserInputCard({ activity, busy, onResolve, handledBelow }: { activity: ConversationActivity; busy: boolean; onResolve(requestId: string, action: "accept" | "decline" | "cancel", content?: Record<string, unknown>): Promise<void>; handledBelow?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const schema = activity.detail?.schema;
	const [values, setValues] = useState<Record<string, unknown>>(() => Object.fromEntries(Object.entries(schema?.properties ?? {}).map(([key, property]) => [key, initialInputValue(property)])));
	const [validationError, setValidationError] = useState<string>();
	const [submitting, setSubmitting] = useState(false);
	const [submitError, setSubmitError] = useState<string>();
	const [activeQuestion, setActiveQuestion] = useState(0);
	const pending = activity.status === "pending";
	const url = activity.detail?.inputMode === "url" ? safeHttpURL(activity.detail.url) : undefined;
	const properties = Object.entries(schema?.properties ?? {});
	const questionGroups = groupedQuestionInputs(properties);
	const visibleProperties = questionGroups?.[activeQuestion] ?? properties;
	const hasPreviousQuestion = Boolean(questionGroups && activeQuestion > 0);
	const hasNextQuestion = Boolean(questionGroups && activeQuestion < questionGroups.length - 1);
	const step = elicitationStepPresentation(activeQuestion, questionGroups?.length ?? 1);
	const promptCopy = elicitationPromptCopy(activity.detail?.message || schema?.description || activity.summary);
	if (pending && handledBelow) return <RequestEcho title={step.status} detail={promptCopy} />;
	const resolve = async (action: "accept" | "decline" | "cancel", content?: Record<string, unknown>) => {
		if (submitting || !activity.requestId) return;
		setSubmitting(true);
		setSubmitError(undefined);
		try { await onResolve(activity.requestId, action, content); }
		catch (cause) { setSubmitError(userFacingError(cause)); }
		finally { setSubmitting(false); }
	};
	const submit = () => {
		const visibleRequired = visibleProperties.flatMap(([name]) => schema?.required?.includes(name) ? [name] : []);
		const missing = missingRequiredInputs(visibleRequired, values);
		if (missing.length) { setValidationError(`Complete ${missing.join(", ")} before continuing.`); return; }
		for (const [name, property] of visibleProperties) {
			const problem = validateInput(property, values[name]);
			if (problem) { setValidationError(`${property.title || humanizeInputName(name)} ${problem}.`); return; }
		}
		setValidationError(undefined);
		if (hasNextQuestion) {
			setActiveQuestion((current) => current + 1);
			return;
		}
		void resolve("accept", values);
	};
	return <View style={pending ? styles.inputRequest : [styles.requestCard, styles.requestCardResolved]}>
		<View style={styles.inputRequestStatus}><View style={[styles.inputRequestDot, { backgroundColor: pending ? t.amber : t.textFaint }]} /><Text style={styles.inputRequestStatusText}>{pending ? step.status : "Input resolved"}</Text></View>
		{promptCopy ? <Text style={styles.requestCopy}>{promptCopy}</Text> : null}
		{pending && activity.detail?.inputMode === "url" ? <View style={styles.urlBox}><Text selectable style={styles.urlText}>{url?.href ?? "The provider supplied an unsafe or invalid URL."}</Text></View> : null}
		{pending && activity.detail?.inputMode !== "url" && schema?.properties ? <View style={styles.form}>
			{visibleProperties.map(([name, property]) => <InputField key={name} name={name} property={property} required={schema.required?.includes(name) ?? false} value={values[name]} onChange={(value) => { setValidationError(undefined); setValues((old) => ({ ...old, [name]: value })); }} />)}
		</View> : null}
		{validationError ? <Text accessibilityRole="alert" style={styles.validation}>{validationError}</Text> : null}
		{submitError ? <Text accessibilityRole="alert" style={styles.validation}>{submitError}</Text> : null}
		{pending && activity.requestId ? <View style={styles.inputActions}>
			<ElicitationAction label="Cancel" disabled={busy || submitting} onPress={() => void resolve("cancel")} />
			<ElicitationAction label={activity.detail?.inputMode === "url" ? "Decline" : "Skip"} disabled={busy || submitting} onPress={() => void resolve("decline")} />
			<View style={{ flex: 1 }} />
			{activity.detail?.inputMode !== "url" && hasPreviousQuestion ? <ElicitationAction label="Back" disabled={busy || submitting} onPress={() => { setValidationError(undefined); setActiveQuestion((current) => Math.max(0, current - 1)); }} /> : null}
			{activity.detail?.inputMode === "url" ? <ElicitationAction label={submitting ? "Opening…" : "Open link"} primary disabled={busy || submitting || !url} onPress={() => {
				if (!url) return;
				void Linking.openURL(url.href)
					.then(() => resolve("accept"))
					.catch(() => setValidationError("This link could not be opened on this device."));
			}} /> : <ElicitationAction label={submitting ? "Sending…" : step.primaryLabel} primary disabled={busy || submitting} onPress={submit} />}
		</View> : pending ? <Text style={[styles.partial, { color: t.amber }]}>This request has no provider identity, so {PRODUCT_NAME} cannot answer it safely. Open diagnostics on the host.</Text> : <Text style={styles.partial}>Already answered. This card is kept for the record.</Text>}
	</View>;
}

export function InputField({ name, property, required, value, onChange }: { name: string; property: InputProperty; required: boolean; value: unknown; onChange(value: unknown): void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const label = `${property.title || humanizeInputName(name)}${required ? " *" : ""}`;
	if (property.type === "boolean") return <View style={styles.switchRow}><Text style={styles.inputLabel}>{label}</Text><Switch accessibilityLabel={label} value={Boolean(value)} onValueChange={onChange} trackColor={{ true: t.accent }} /></View>;
	const options = inputOptions(property);
	if (options.length) {
		const multi = property.type === "array";
		return <View style={styles.field}>
			<Text style={styles.inputEyebrow}>{label}</Text>
			{property.description ? <Text style={styles.inputQuestion}>{property.description}</Text> : null}
			<ElicitationChoiceList choices={options} multi={multi} selected={(choice) => multi ? Array.isArray(value) && value.includes(choice) : value === choice} onChange={(choice) => onChange(multi ? toggleInputValue(Array.isArray(value) ? value : [], choice) : choice)} />
		</View>;
	}
	return <View style={styles.field}>
		<Text style={styles.inputLabel}>{label}</Text>
		{property.description ? <Text style={styles.inputHint}>{property.description}</Text> : null}
		<ElicitationTextField label={label} value={value} numeric={property.type === "number" || property.type === "integer"} maxLength={property.maxLength} onChange={onChange} />
	</View>;
}


export function Action({ label, hint, onPress, primary, tone, disabled }: { label: string; hint?: string; onPress(): void; primary?: boolean; tone?: "danger"; disabled?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const fill = tone === "danger" ? t.tintRed : primary ? t.accent : t.bgElevated;
	const ink = tone === "danger" ? t.red : primary ? t.onAccent : t.textPrimary;
	return <Pressable accessibilityRole="button" accessibilityState={{ disabled }} disabled={disabled} onPress={() => { if (tone === "danger") haptics.warning(); else haptics.tap(); onPress(); }} style={({ pressed }) => [styles.action, { backgroundColor: fill }, pressed && { opacity: 0.75 }, disabled && { opacity: 0.45 }]}><Text style={[styles.actionLabel, { color: ink }]}>{label}</Text>{hint ? <Text style={styles.actionHint}>{hint}</Text> : null}</Pressable>;
}
