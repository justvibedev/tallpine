/** The form variant of a pending elicitation request. */
import { useEffect, useId, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { ArrowUp, ChevronLeft, Loader2 } from "lucide-react";
import { cn } from "../../../lib/utils";
import type { ConversationActivity } from "../../../types/conversation";
import { ACCENT_ACTION_PILL } from "../action-pill";
import { Spinner } from "../../ui/spinner";
import { InputAction, claudeQuestionGroups, otherAnswerFields, InputValue, initialValues, questionPrompt, isEmpty, isBlank } from "./elicitation-model";
import { DockHeader, DockFooter } from "./ElicitationDockParts";
import { FormField } from "./ElicitationFormField";

export function FormRequest({
	activity,
	disabled,
	onResolve,
}: {
	activity: ConversationActivity;
	disabled: boolean;
	onResolve: (action: InputAction, content?: Record<string, unknown>) => Promise<void>;
}) {
	const schema = activity.detail?.schema;
	const properties = useMemo(() => Object.entries(schema?.properties ?? {}), [schema?.properties]);
	const questionGroups = useMemo(() => claudeQuestionGroups(properties), [properties]);
	const otherAnswers = useMemo(() => otherAnswerFields(properties), [properties]);
	const required = useMemo(() => new Set(schema?.required ?? []), [schema?.required]);
	const [values, setValues] = useState<Record<string, InputValue>>(() => initialValues(properties));
	const [missing, setMissing] = useState<Set<string>>(new Set());
	const [activeQuestion, setActiveQuestion] = useState(0);
	const [collapsed, setCollapsed] = useState(false);
	const visibleProperties = questionGroups?.[activeQuestion] ?? properties;
	const hasPreviousQuestion = questionGroups !== undefined && activeQuestion > 0;
	const hasNextQuestion = questionGroups !== undefined && activeQuestion < questionGroups.length - 1;
	const headerId = useId();
	const formRef = useRef<HTMLFormElement>(null);
	const shownQuestion = useRef(activeQuestion);

	// Changing question unmounts the focused choice (and Back, on the first
	// question), which drops focus out of the card. Put it on the new question so
	// the next number key and Enter still reach it.
	useEffect(() => {
		if (shownQuestion.current === activeQuestion) return;
		shownQuestion.current = activeQuestion;
		const form = formRef.current;
		if (!form || form.contains(document.activeElement)) return;
		(form.querySelector<HTMLInputElement>("input:checked") ?? form.querySelector<HTMLInputElement>("input"))?.focus();
	}, [activeQuestion]);

	// Claude puts a single prompt in message and each prompt of a multi-question
	// form in its field description. The field title is only a short topic label.
	const question = questionGroups ? visibleProperties[0] : undefined;
	const title = question
		? questionPrompt(question, questionGroups?.length === 1 ? activity.detail?.message : undefined)
		: activity.detail?.message ?? schema?.title ?? activity.summary;
	const pager =
		questionGroups && questionGroups.length > 1
			? `${activeQuestion + 1} of ${questionGroups.length}`
			: undefined;

	// Number keys pick the matching choice and 0 jumps to "Other", the same
	// shortcuts the rows print. Typing in a text field is left alone.
	function pickByNumber(event: KeyboardEvent<HTMLFormElement>) {
		if (!questionGroups || disabled || event.metaKey || event.ctrlKey || event.altKey) return;
		const onChoice = event.target instanceof HTMLInputElement
			&& (event.target.type === "radio" || event.target.type === "checkbox");
		if (event.target instanceof HTMLInputElement && !onChoice) return;
		const form = event.currentTarget;
		// Browsers don't submit a form when Enter is pressed on a radio.
		if (event.key === "Enter" && onChoice) {
			event.preventDefault();
			form.requestSubmit();
			return;
		}
		if (!/^[0-9]$/.test(event.key)) return;
		const target = event.key === "0"
			? form.querySelector<HTMLInputElement>("input[type=text], input[type=number]")
			: form.querySelectorAll<HTMLInputElement>("input[type=radio], input[type=checkbox]")[Number(event.key) - 1];
		if (!target) return;
		event.preventDefault();
		if (event.key === "0") target.focus();
		else target.click();
	}

	function submit(event: FormEvent) {
		event.preventDefault();
		// A marked Other answer stands in for its question's choice; the daemon
		// accepts it in the question's place on the same marker.
		const absent = new Set(
			visibleProperties
				.filter(([name]) => {
					if (!required.has(name) || !isEmpty(values[name])) return false;
					const other = otherAnswers.get(name);
					return other === undefined || isBlank(values[other]);
				})
				.map(([name]) => name),
		);
		setMissing(absent);
		if (absent.size > 0) return;
		if (hasNextQuestion) {
			setActiveQuestion((current) => current + 1);
			return;
		}
		void onResolve("accept", values);
	}

	return (
		<form ref={formRef} onSubmit={submit} onKeyDown={pickByNumber}>
			<DockHeader
				id={headerId}
				title={title}
				pager={pager}
				disabled={disabled}
				onDismiss={() => void onResolve("cancel")}
				collapsed={collapsed}
				onToggleCollapsed={questionGroups ? () => setCollapsed((current) => !current) : undefined}
			/>
			{collapsed ? null : (
				<>
				{!questionGroups && schema?.description ? (
					<p className="px-3 pb-1 text-caption leading-relaxed text-muted-foreground">{schema.description}</p>
				) : null}
				<div className={cn(questionGroups ? "flex flex-col" : "flex flex-col gap-3 px-3 pb-1")}>
					{visibleProperties.map(([name, property], index) => (
						<FormField
							key={name}
							name={name}
							property={property}
							value={values[name]}
							required={required.has(name)}
							invalid={missing.has(name)}
							disabled={disabled}
							// Inside a Claude question the header is the prompt, so the first
							// field borrows it as its accessible name instead of printing it.
							labelledBy={questionGroups && index === 0 ? headerId : undefined}
							rows={Boolean(questionGroups)}
							onChange={(value) => {
								// A choice and its marked "Other" answer are alternatives: picking
								// one clears the other so it is always clear which one is sent.
								// Unmarked fields are independent, whatever their names.
								const question = [...otherAnswers].find(([, other]) => other === name)?.[0];
								const sibling = question ?? otherAnswers.get(name);
								setValues((current) => {
									const next: Record<string, InputValue> = { ...current, [name]: value };
									const singleChoice = sibling && !Array.isArray(current[question ?? name]) && !Array.isArray(value);
									if (singleChoice && !isBlank(value) && sibling in current) delete next[sibling];
									return next;
								});
								setMissing((current) => {
									if (!current.has(name)) return current;
									const next = new Set(current);
									next.delete(name);
									return next;
								});
							}}
						/>
					))}
				</div>
				<DockFooter>
					<div className="flex items-center gap-1">
						{hasPreviousQuestion ? (
							<button
								type="button"
								aria-label="Back"
								title="Back"
								className="grid size-7 place-items-center rounded-full text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground disabled:opacity-50"
								disabled={disabled}
								onClick={() => {
									setMissing(new Set());
									setActiveQuestion((current) => current - 1);
								}}
							>
								<ChevronLeft aria-hidden="true" className="size-4" />
							</button>
						) : null}
						{questionGroups && questionGroups.length > 1 ? (
							<div className="flex items-center gap-1 px-1" aria-hidden="true">
								{questionGroups.map((group, step) => (
									<button
										key={group[0]?.[0] ?? step}
										type="button"
										tabIndex={-1}
										disabled={disabled || step >= activeQuestion}
										onClick={() => {
											setMissing(new Set());
											setActiveQuestion(step);
										}}
										className={cn(
											"size-1.5 rounded-full transition-colors",
											step === activeQuestion
												? "bg-foreground"
												: step < activeQuestion
													? "bg-logo-accent hover:bg-logo-accent-bright"
													: "bg-border-strong",
										)}
									/>
								))}
							</div>
						) : null}
						<button
							type="button"
							className="h-7 rounded-full px-2 text-xs text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground disabled:opacity-50"
							disabled={disabled}
							onClick={() => onResolve("decline")}
						>
							Skip
						</button>					</div>
					{/* Sending an answer is the composer's send, so it wears the composer's send colour. */}
					<button
						type="submit"
						className={cn(
							ACCENT_ACTION_PILL,
							"min-w-20 justify-center bg-foreground text-background hover:bg-foreground/90",
						)}
						disabled={disabled}
					>
						{disabled ? (
							<Spinner icon={Loader2} aria-label="Sending answer" className="size-3.5" />
						) : hasNextQuestion ? (
							"Next"
						) : (
							<>
								Continue
								<ArrowUp aria-hidden="true" className="size-3.5" />
							</>
						)}
					</button>
				</DockFooter>
				</>
			)}
		</form>
	);
}
