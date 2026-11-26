// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { z } from "zod";

export const structuredInvocationSchema = z
    .object({
        inputSchema: z.unknown().optional(),
        outputSchema: z.unknown().optional(),
    })
    .optional();

export const acknowledgmentPolicySchema = z
    .object({
        mode: z.enum(["on_receive", "on_completion"]),
        onFailure: z
            .object({
                action: z.enum(["nack", "ack"]),
                nackOutcome: z.enum(["rejected", "failed"]).optional(),
            })
            .optional(),
        timeoutSeconds: z.number().positive("Timeout must be a positive number").optional(),
    })
    .optional();

const subscriptionSchema = z.object({
    topic: z.string(),
});

const outputSchema = z.object({
    enabled: z.boolean(),
    topic: z.string(),
    topicType: z.enum(["static", "dynamic"]).optional(),
});

export const createEventRuleSchema = (existingNames: string[], isEditing: boolean, targetType: "agent" | "workflow") =>
    z
        .object({
            name: z
                .string()
                .trim()
                .min(1, "Name is required")
                .regex(/^[a-zA-Z]\w*$/, "Must start with a letter and contain only letters, numbers, and underscores")
                .refine(val => isEditing || !existingNames.some(name => name.toLowerCase() === val.toLowerCase()), "A rule with this name already exists"),
            subscriptions: z.array(subscriptionSchema).refine(subs => subs.some(s => s.topic.trim().length > 0), "At least one topic is required"),
            messageFormat: z.enum(["json", "text"]),
            targetAgent: z.string(),
            promptTemplate: z.string(),
            targetWorkflowName: z.string().optional(),
            structuredInvocation: structuredInvocationSchema,
            acknowledgmentPolicy: acknowledgmentPolicySchema,
            successOutput: outputSchema.refine(output => !output.enabled || output.topic.trim().length > 0, { message: "Topic is required when enabled", path: ["topic"] }),
            errorOutput: outputSchema.refine(output => !output.enabled || output.topic.trim().length > 0, { message: "Topic is required when enabled", path: ["topic"] }),
        })
        .superRefine((data, ctx) => {
            if (targetType === "agent") {
                if (!data.targetAgent?.trim()) {
                    ctx.addIssue({ code: "custom", message: "Agent is required", path: ["targetAgent"] });
                }
                if (!data.promptTemplate?.trim()) {
                    ctx.addIssue({ code: "custom", message: "Additional Instructions are required", path: ["promptTemplate"] });
                }
            } else if (!data.targetWorkflowName?.trim()) {
                ctx.addIssue({ code: "custom", message: "Workflow Name is required", path: ["targetWorkflowName"] });
            }
        });

export type EventRuleFormData = z.infer<ReturnType<typeof createEventRuleSchema>>;

const EVENT_RULE_FIELD_LABELS: Record<string, string> = {
    name: "Name",
    subscriptions: "Topics",
    messageFormat: "Message Format",
    targetAgent: "Agent",
    targetWorkflowName: "Workflow Name",
    promptTemplate: "Additional Instructions",
    structuredInvocation: "Structured Invocation",
    acknowledgmentPolicy: "Event Acknowledgment",
    successOutput: "Success Response",
    errorOutput: "Error Response",
};

export const eventRuleErrorLabels = (errorFields: string[]): string[] => errorFields.map(field => EVENT_RULE_FIELD_LABELS[field] ?? field).filter((label, index, all) => all.indexOf(label) === index);

export const eventRuleSubmitErrorMessage = (errorFields: string[]): string => {
    const labels = eventRuleErrorLabels(errorFields);
    return labels.length > 0 ? `Cannot save. Please correct: ${labels.join(", ")}.` : "Cannot save. Please correct the highlighted fields.";
};
