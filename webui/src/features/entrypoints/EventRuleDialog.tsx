// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { CircleCheckBig, CircleX, ExternalLink, Plus, X } from "lucide-react";
import { Controller, type FieldErrors, useFieldArray, useForm } from "react-hook-form";

import { JsonSchemaField, RadioOptions } from "@/common/form";
import { HelpPopover } from "@/common/HelpPopover";
import { PageLabel, PageLabelWithValue, PageSection } from "@/common/layout";
import { ErrorLabel } from "@/common/status";

import { cn } from "@/utils/cnTailwind";
import { EXTERNAL_DOC_LINKS } from "@/utils/docs";

import { Button } from "@/ui/button";
import { Checkbox } from "@/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";
import { Input } from "@/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";
import { Textarea } from "@/ui/textarea";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";
import type { EventRule } from "@/types/models";
import { DEFAULT_EVENT_RULE } from "@/types/models";

import { MessageBanner } from "@/common";

import { createEventRuleSchema, type EventRuleFormData, eventRuleSubmitErrorMessage } from "./eventRuleSchemas";
import { type AgentOption, isAgentNotDeployed } from "./hooks";

interface EventRuleDialogProps {
    open: boolean;
    onClose: () => void;
    onSave: (rule: EventRule) => void;
    initialRule: EventRule | null;
    agents: AgentOption[];
    existingNames: string[];
    originalRuleName?: string;
    readOnly?: boolean;
}

export const EventRuleDialog = ({ open, onClose, onSave, initialRule, agents, existingNames, originalRuleName, readOnly = false }: EventRuleDialogProps) => {
    const isCloning = originalRuleName !== undefined;
    const isEditing = initialRule !== null && !isCloning;
    const filteredExistingNames = isEditing ? existingNames.filter(n => n.toLowerCase() !== initialRule.name.toLowerCase()) : existingNames;

    const [targetType, setTargetType] = useState<"agent" | "workflow">("agent");
    const [structuredEnabled, setStructuredEnabled] = useState(false);
    const [deferredAckEnabled, setDeferredAckEnabled] = useState(false);
    const [submitError, setSubmitError] = useState<string | null>(null);

    const schema = useMemo(() => createEventRuleSchema(filteredExistingNames, isEditing, targetType), [filteredExistingNames, isEditing, targetType]);

    const {
        register,
        control,
        handleSubmit,
        reset,
        watch,
        setValue,
        getValues,
        setError,
        clearErrors,
        formState: { errors },
    } = useForm<EventRuleFormData>({
        resolver: zodResolver(schema),
        defaultValues: DEFAULT_EVENT_RULE,
        mode: "onSubmit",
        reValidateMode: "onChange",
    });

    const {
        fields: subscriptionFields,
        prepend: prependSubscription,
        remove: removeSubscription,
    } = useFieldArray({
        control,
        name: "subscriptions",
    });

    const successOutputEnabled = watch("successOutput.enabled");
    const errorOutputEnabled = watch("errorOutput.enabled");
    const selectedAgent = watch("targetAgent");

    const selectableAgents = useMemo(() => {
        const deployed = agents.filter(agent => !isAgentNotDeployed(agent));
        if (!selectedAgent || deployed.some(agent => agent.name === selectedAgent)) return deployed;
        const pinned = agents.find(agent => agent.name === selectedAgent);
        return pinned ? [...deployed, pinned] : deployed;
    }, [agents, selectedAgent]);

    useEffect(() => {
        if (!open) return;
        setSubmitError(null);
        reset(initialRule ?? { ...DEFAULT_EVENT_RULE, targetAgent: agents.find(agent => !isAgentNotDeployed(agent))?.name ?? "" });
        setTargetType(initialRule?.targetWorkflowName ? "workflow" : "agent");
        setStructuredEnabled(!!initialRule?.structuredInvocation?.inputSchema || !!initialRule?.structuredInvocation?.outputSchema);
        setDeferredAckEnabled(initialRule?.acknowledgmentPolicy?.mode === "on_completion");
    }, [open, initialRule, agents, reset]);

    const handleDeferredAckToggle = (enabled: boolean) => {
        setDeferredAckEnabled(enabled);
        if (enabled) {
            setValue("acknowledgmentPolicy.mode", "on_completion");
            if (!getValues("acknowledgmentPolicy.timeoutSeconds")) {
                setValue("acknowledgmentPolicy.timeoutSeconds", 300);
            }
        }
    };

    const handleTargetTypeChange = (nextTargetType: "agent" | "workflow") => {
        setTargetType(nextTargetType);
        const hiddenTargetField = nextTargetType === "workflow" ? "targetAgent" : "targetWorkflowName";
        setValue(hiddenTargetField, "");
        clearErrors(hiddenTargetField);
    };

    const handleInvalidSubmit = useCallback((formErrors: FieldErrors<EventRuleFormData>) => {
        setSubmitError(eventRuleSubmitErrorMessage(Object.keys(formErrors)));
    }, []);

    const onSubmit = (data: EventRuleFormData) => {
        setSubmitError(null);
        const mergedSuccessOutput = {
            ...(initialRule?.successOutput ?? {}),
            ...(data.successOutput ?? {}),
        };
        const mergedErrorOutput = {
            ...(initialRule?.errorOutput ?? {}),
            ...(data.errorOutput ?? {}),
        };
        const cleanedRule: Partial<EventRule> & Omit<EventRule, "targetAgent" | "promptTemplate" | "targetWorkflowName"> = {
            ...(initialRule ?? {}),
            ...data,
            name: data.name.trim(),
            subscriptions: data.subscriptions.filter(s => s.topic.trim()),
            structuredInvocation: data.structuredInvocation as EventRule["structuredInvocation"],
            successOutput: mergedSuccessOutput as EventRule["successOutput"],
            errorOutput: mergedErrorOutput as EventRule["errorOutput"],
        };

        if (targetType === "workflow") {
            delete cleanedRule.targetAgent;
            delete cleanedRule.promptTemplate;
        } else {
            delete cleanedRule.targetWorkflowName;
        }

        if (!structuredEnabled) {
            delete cleanedRule.structuredInvocation;
        } else if (cleanedRule.structuredInvocation) {
            const { inputSchema, outputSchema } = cleanedRule.structuredInvocation;
            const inputIsValid = inputSchema === undefined || (typeof inputSchema === "object" && inputSchema !== null && !Array.isArray(inputSchema));
            const outputIsValid = outputSchema === undefined || (typeof outputSchema === "object" && outputSchema !== null && !Array.isArray(outputSchema));
            if (!inputIsValid || !outputIsValid) {
                if (!inputIsValid) setError("structuredInvocation.inputSchema", { message: "Must be a valid JSON object" });
                if (!outputIsValid) setError("structuredInvocation.outputSchema", { message: "Must be a valid JSON object" });
                return;
            }
            if (inputSchema === undefined && outputSchema === undefined) {
                delete cleanedRule.structuredInvocation;
            }
        }

        if (!deferredAckEnabled) {
            delete cleanedRule.acknowledgmentPolicy;
        } else {
            const failureAction = data.acknowledgmentPolicy?.onFailure?.action ?? "nack";
            cleanedRule.acknowledgmentPolicy = {
                mode: "on_completion",
                timeoutSeconds: data.acknowledgmentPolicy?.timeoutSeconds ?? 300,
                onFailure: {
                    action: failureAction,
                    ...(failureAction === "nack" && {
                        nackOutcome: data.acknowledgmentPolicy?.onFailure?.nackOutcome ?? "rejected",
                    }),
                },
            };
        }

        onSave(cleanedRule as EventRule);
    };

    return (
        <Dialog open={open} onOpenChange={isOpen => !isOpen && onClose()}>
            <DialogContent className="flex max-h-[90vh] max-w-[80vw] flex-col pr-2 sm:max-w-[80vw]">
                <DialogHeader className="mb-2 shrink-0">
                    <DialogTitle>{readOnly ? "View Event Rule" : isCloning ? `Clone ${originalRuleName}` : isEditing ? "Edit Event Rule" : "Add Event Rule"}</DialogTitle>
                    <DialogDescription>Event rules define how agents process incoming events and where they publish responses.</DialogDescription>
                </DialogHeader>

                <form className="flex min-h-0 flex-1 flex-col">
                    <div className="flex min-h-0 flex-1 flex-col gap-6 overflow-y-auto py-6 pr-6">
                        <PageLabelWithValue>
                            <PageLabel required>Name</PageLabel>
                            <Input {...register("name")} readOnly={readOnly} aria-invalid={!!errors?.name} />
                            {errors.name && <ErrorLabel message={errors.name.message} />}
                        </PageLabelWithValue>

                        <PageSection>
                            <div className="py-2 text-lg font-semibold">Incoming Events</div>
                            <div className="flex items-center gap-1 text-sm text-(--secondary-text-wMain)">
                                Define the topics where agents will process incoming events and how those events are formatted.
                                <HelpPopover
                                    content={
                                        <>
                                            <div className="font-semibold">Topic Addresses</div>
                                            <div>{"You can use multi-level (>) and single-level (*) wildcards."}</div>
                                            <Button variant="link" className="mt-1 h-3 p-0!" onClick={() => window.open(EXTERNAL_DOC_LINKS.topicWildcards)}>
                                                Learn more about topic wildcards.
                                                <ExternalLink size={3} />
                                            </Button>
                                            <div className="pt-4 font-semibold">Message Format</div>
                                            <div>Select how incoming messages should be interpreted by the agent: JSON or plain text.</div>
                                            <div className="pt-4 font-semibold">Delivery Mode</div>
                                            <div>
                                                While the entrypoint is running, a temporary queue is created on your event broker to receive events. If the entrypoint goes offline, this queue is removed and any events published during the downtime
                                                will not be captured.
                                            </div>
                                        </>
                                    }
                                />
                            </div>
                            <div className="flex items-baseline justify-between">
                                <PageLabel required>Topics</PageLabel>
                                {!readOnly && (
                                    <Button testid="addTopic" type="button" variant="ghost" onClick={() => prependSubscription({ topic: "" })}>
                                        <Plus />
                                        Add Topic
                                    </Button>
                                )}
                            </div>
                            <div className="flex flex-col gap-2">
                                {subscriptionFields.map((field, index) => {
                                    const topicValue = watch(`subscriptions.${index}.topic`);
                                    const hasTopicError = errors.subscriptions?.root && (!topicValue || topicValue.trim() === "");

                                    return (
                                        <div key={field.id} className="flex gap-2">
                                            <Input {...register(`subscriptions.${index}.topic`)} readOnly={readOnly} aria-invalid={hasTopicError} />
                                            {!readOnly && (
                                                <Button type="button" variant="ghost" onClick={() => removeSubscription(index)} disabled={subscriptionFields.length === 1}>
                                                    <X />
                                                </Button>
                                            )}
                                        </div>
                                    );
                                })}
                            </div>
                            {errors.subscriptions?.root && <ErrorLabel message={errors.subscriptions.root.message} />}

                            <PageLabel className="mt-4" required>
                                Message Format
                            </PageLabel>
                            <Controller
                                name="messageFormat"
                                control={control}
                                render={({ field }) => (
                                    <RadioOptions
                                        options={[
                                            { value: "json", label: "JSON" },
                                            { value: "text", label: "Text" },
                                        ]}
                                        value={field.value}
                                        onChange={field.onChange}
                                        disabled={readOnly}
                                    />
                                )}
                            />
                        </PageSection>

                        <PageSection>
                            <div className="py-2 text-lg font-semibold">Target</div>
                            <div className="text-sm text-(--secondary-text-wMain)">Choose whether events are processed by an agent or routed to a workflow.</div>

                            <PageLabel required>Target Type</PageLabel>
                            <RadioOptions
                                options={[
                                    { value: "agent", label: "Agent" },
                                    { value: "workflow", label: "Workflow" },
                                ]}
                                value={targetType}
                                onChange={val => handleTargetTypeChange(val as "agent" | "workflow")}
                                disabled={readOnly}
                            />

                            {targetType === "agent" ? (
                                <>
                                    <PageLabelWithValue className="mt-4">
                                        <PageLabel required>Agent</PageLabel>
                                        <Controller
                                            name="targetAgent"
                                            control={control}
                                            render={({ field }) => (
                                                <Select readonly={readOnly} value={field.value} onValueChange={field.onChange}>
                                                    <SelectTrigger readonly={readOnly} className={cn("w-1/2", errors.targetAgent && "border-(--error-w100)")}>
                                                        <SelectValue />
                                                    </SelectTrigger>
                                                    <SelectContent>
                                                        {selectableAgents.map(agent => (
                                                            <SelectItem key={agent.name} value={agent.name}>
                                                                {agent.displayName}
                                                            </SelectItem>
                                                        ))}
                                                    </SelectContent>
                                                </Select>
                                            )}
                                        />
                                        {errors.targetAgent && <ErrorLabel message={errors.targetAgent.message} />}
                                    </PageLabelWithValue>

                                    <PageLabelWithValue className="mt-4">
                                        <PageLabel required>Additional Instructions</PageLabel>
                                        <div className="flex items-center gap-1 text-sm text-(--secondary-text-wMain)">
                                            Provide additional instructions to help the agent correctly handle this event. These instructions are combined with the agent&apos;s system prompt.
                                            <HelpPopover
                                                content={
                                                    <>
                                                        <div className="font-semibold">Placeholders</div>
                                                        <div>{"Placeholder terms: {payload} and {topic} can be used within these instructions to tell the agent how to process event data."}</div>
                                                        <div className="pt-1">{"If the incoming message format is JSON, you can use the {payload.field} syntax to access individual properties."}</div>
                                                        <div className="pt-1">{`For example, to access a "requestedBy" field in a JSON message, specify {payload.requestedBy}.`}</div>
                                                    </>
                                                }
                                            />
                                        </div>
                                        <Textarea id="promptTemplate" {...register("promptTemplate")} readOnly={readOnly} rows={4} className={cn("ml-0.5", errors.promptTemplate && "border-(--error-w100)")} />
                                        {errors.promptTemplate && <ErrorLabel message={errors.promptTemplate.message} />}
                                    </PageLabelWithValue>
                                </>
                            ) : (
                                <PageLabelWithValue className="mt-4">
                                    <PageLabel required>Workflow Name</PageLabel>
                                    <div className="text-sm text-(--secondary-text-wMain)">The name of the workflow to invoke when an event is received.</div>
                                    <Input {...register("targetWorkflowName")} readOnly={readOnly} aria-invalid={!!errors?.targetWorkflowName} placeholder="e.g. CompleteOrderWorkflow" />
                                    {errors.targetWorkflowName && <ErrorLabel message={errors.targetWorkflowName.message} />}
                                </PageLabelWithValue>
                            )}
                        </PageSection>

                        <PageSection>
                            <div className="py-2 text-lg font-semibold">Schemas</div>
                            <div className="flex items-center gap-1 text-sm text-(--secondary-text-wMain)">
                                Optionally define JSON schemas for structured request and response data.
                                <HelpPopover
                                    content={
                                        <>
                                            <div>
                                                When enabled, the incoming event data will be validated against the input schema before being sent to the agent or workflow. The response will also be constrained to conform to the specified output
                                                schema.
                                            </div>
                                            <div className="pt-2">Both schemas should be valid JSON Schema objects. Either schema can be omitted if validation is only needed in one direction.</div>
                                        </>
                                    }
                                />
                            </div>

                            <div className="rounded-xs border p-4">
                                <div className="flex items-center gap-4">
                                    <Tooltip>
                                        <TooltipTrigger asChild>
                                            <div>
                                                <Checkbox checked={structuredEnabled} onCheckedChange={val => setStructuredEnabled(!!val)} disabled={readOnly} />
                                            </div>
                                        </TooltipTrigger>
                                        <TooltipContent>{structuredEnabled ? "Disable" : "Enable"}</TooltipContent>
                                    </Tooltip>
                                    <PageLabel className="font-semibold">Apply input and/or output schemas</PageLabel>
                                </div>

                                {structuredEnabled && (
                                    <div className="flex flex-col gap-4 pt-4">
                                        <div>
                                            <Controller name="structuredInvocation.inputSchema" control={control} render={({ field }) => <JsonSchemaField label="Input Schema (JSON)" field={field} readOnly={readOnly} />} />
                                            {errors.structuredInvocation?.inputSchema && <ErrorLabel message={errors.structuredInvocation.inputSchema.message} />}
                                        </div>
                                        <div>
                                            <Controller name="structuredInvocation.outputSchema" control={control} render={({ field }) => <JsonSchemaField label="Output Schema (JSON)" field={field} readOnly={readOnly} />} />
                                            {errors.structuredInvocation?.outputSchema && <ErrorLabel message={errors.structuredInvocation.outputSchema.message} />}
                                        </div>
                                    </div>
                                )}
                            </div>
                        </PageSection>

                        <PageSection>
                            <div className="py-2 text-lg font-semibold">Event Acknowledgment</div>
                            <div className="text-sm text-(--secondary-text-wMain)">Control when incoming events are acknowledged on the broker.</div>

                            <div className="rounded-xs border p-4">
                                <div className="flex items-center gap-4">
                                    <Tooltip>
                                        <TooltipTrigger asChild>
                                            <div>
                                                <Checkbox checked={deferredAckEnabled} onCheckedChange={val => handleDeferredAckToggle(!!val)} disabled={readOnly} />
                                            </div>
                                        </TooltipTrigger>
                                        <TooltipContent>{deferredAckEnabled ? "Disable" : "Enable"}</TooltipContent>
                                    </Tooltip>
                                    <PageLabel className="font-semibold">Defer acknowledgment until processing completes</PageLabel>
                                </div>

                                {deferredAckEnabled && (
                                    <div className="flex flex-col gap-4 pt-4">
                                        <PageLabelWithValue>
                                            <PageLabel>Timeout (seconds)</PageLabel>
                                            <Input type="number" {...register("acknowledgmentPolicy.timeoutSeconds", { valueAsNumber: true })} readOnly={readOnly} className="w-32" />
                                            {errors.acknowledgmentPolicy?.timeoutSeconds && <ErrorLabel message={errors.acknowledgmentPolicy.timeoutSeconds.message} />}
                                        </PageLabelWithValue>

                                        <div className="flex items-center gap-1">
                                            <PageLabel>On Failure Action</PageLabel>
                                            <HelpPopover
                                                content={
                                                    <>
                                                        <div className="font-semibold">NACK</div>
                                                        <div>Sends a negative acknowledgment back to the broker, indicating the event was not successfully processed. The broker can then redeliver or move the event depending on the NACK outcome.</div>
                                                        <div className="pt-2 font-semibold">ACK</div>
                                                        <div>Acknowledges the event even though processing failed. The event will not be redelivered. Use this when failures should be silently discarded.</div>
                                                    </>
                                                }
                                            />
                                        </div>
                                        <Controller
                                            name="acknowledgmentPolicy.onFailure.action"
                                            control={control}
                                            defaultValue="nack"
                                            render={({ field }) => (
                                                <RadioOptions
                                                    options={[
                                                        { value: "nack", label: "NACK" },
                                                        { value: "ack", label: "ACK" },
                                                    ]}
                                                    value={field.value ?? "nack"}
                                                    onChange={field.onChange}
                                                    disabled={readOnly}
                                                />
                                            )}
                                        />

                                        {(watch("acknowledgmentPolicy.onFailure.action") ?? "nack") === "nack" && (
                                            <>
                                                <div className="mt-2 flex items-center gap-1">
                                                    <PageLabel>NACK Outcome</PageLabel>
                                                    <HelpPopover
                                                        content={
                                                            <>
                                                                <div className="font-semibold">Rejected</div>
                                                                <div>The broker will attempt to redeliver the event. Use this for transient failures where retrying may succeed.</div>
                                                                <div className="pt-2 font-semibold">Failed</div>
                                                                <div>
                                                                    The event is removed from the queue and will not be redelivered. If a Dead Message Queue (DMQ) is configured on the broker, the event will be placed there. Use this for permanent
                                                                    failures where retrying would not help.
                                                                </div>
                                                            </>
                                                        }
                                                    />
                                                </div>
                                                <Controller
                                                    name="acknowledgmentPolicy.onFailure.nackOutcome"
                                                    control={control}
                                                    defaultValue="rejected"
                                                    render={({ field }) => (
                                                        <RadioOptions
                                                            options={[
                                                                { value: "rejected", label: "Rejected" },
                                                                { value: "failed", label: "Failed" },
                                                            ]}
                                                            value={field.value ?? "rejected"}
                                                            onChange={field.onChange}
                                                            disabled={readOnly}
                                                        />
                                                    )}
                                                />
                                            </>
                                        )}
                                    </div>
                                )}
                            </div>
                        </PageSection>

                        <PageSection>
                            <div className="py-2 text-lg font-semibold">Outgoing Responses</div>
                            <div className="text-sm text-(--secondary-text-wMain)">Responses can be published to existing topics.</div>
                            <OutputConfigSection
                                label="Success Response"
                                fieldLabel="The topic where success responses are published."
                                prefix="successOutput"
                                register={register}
                                control={control}
                                enabled={successOutputEnabled}
                                errors={errors.successOutput}
                                readOnly={readOnly}
                            />
                            <OutputConfigSection
                                label="Error Response"
                                fieldLabel="The topic where error responses are published."
                                prefix="errorOutput"
                                register={register}
                                control={control}
                                enabled={errorOutputEnabled}
                                errors={errors.errorOutput}
                                readOnly={readOnly}
                            />
                        </PageSection>
                    </div>

                    <div className="mt-0 shrink-0 flex-col border-t px-6 py-4">
                        {submitError && <MessageBanner variant="error" message={submitError} dismissible onDismiss={() => setSubmitError(null)} className="mb-4" />}
                        <DialogFooter className="p-0">
                            {readOnly ? (
                                <Button data-testid="closeEventRule" type="button" onClick={onClose}>
                                    Close
                                </Button>
                            ) : (
                                <>
                                    <Button data-testid="cancelEventRule" type="button" variant="ghost" onClick={onClose}>
                                        Cancel
                                    </Button>
                                    <Button data-testid="saveEventRule" type="button" onClick={handleSubmit(onSubmit, handleInvalidSubmit)}>
                                        {isCloning ? "Clone" : isEditing ? "Update" : "Add"}
                                    </Button>
                                </>
                            )}
                        </DialogFooter>
                    </div>
                </form>
            </DialogContent>
        </Dialog>
    );
};

interface OutputConfigSectionProps {
    label: string;
    fieldLabel: string;
    prefix: "successOutput" | "errorOutput";
    register: ReturnType<typeof useForm<EventRuleFormData>>["register"];
    control: ReturnType<typeof useForm<EventRuleFormData>>["control"];
    enabled: boolean;
    errors?: {
        topic?: { message?: string };
    };
    readOnly?: boolean;
}

const OutputConfigSection = ({ label, fieldLabel, prefix, register, control, enabled, errors, readOnly = false }: OutputConfigSectionProps) => {
    return (
        <div className="rounded-xs border p-4">
            <div className="flex items-center gap-4">
                <Controller
                    name={`${prefix}.enabled`}
                    control={control}
                    render={({ field }) => {
                        return (
                            <Tooltip>
                                <TooltipTrigger asChild>
                                    <div>
                                        <Checkbox checked={field.value} onCheckedChange={field.onChange} disabled={readOnly} />
                                    </div>
                                </TooltipTrigger>
                                <TooltipContent>{field.value ? "Disable" : "Enable"}</TooltipContent>
                            </Tooltip>
                        );
                    }}
                />
                <div className="flex gap-2">
                    <PageLabel className="font-semibold">{label}</PageLabel>
                    {prefix === "successOutput" && <CircleCheckBig className="size-5 text-(--success-w100)" />}
                    {prefix === "errorOutput" && <CircleX className="size-5 text-(--error-w100)" />}
                </div>
            </div>

            {enabled && (
                <div className="py-2">
                    <PageLabelWithValue>
                        <PageLabel required>{fieldLabel}</PageLabel>
                        <Input {...register(`${prefix}.topic`)} readOnly={readOnly} aria-invalid={!!errors?.topic} />
                        {errors?.topic && <ErrorLabel message={errors.topic.message} />}
                    </PageLabelWithValue>
                </div>
            )}
        </div>
    );
};
