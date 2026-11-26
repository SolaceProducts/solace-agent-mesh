// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Loader2, Search } from "lucide-react";
import type { Control, FieldError, FieldValues, UseFormRegister } from "react-hook-form";
import { Controller, useFormContext, useWatch } from "react-hook-form";

import { CharacterCountLabel, RadioOptions } from "@/common/form";
import { PageLabel, PageLabelWithValue } from "@/common/layout";
import { ErrorLabel } from "@/common/status";

import { Button } from "@/ui/button";
import { Checkbox } from "@/ui/checkbox";
import { Input } from "@/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";
import { Textarea } from "@/ui/textarea";

import { MessageBanner, PasswordInput } from "@/common";

import { EventRulesList } from "./EventRulesList";
import { useStoredSecrets } from "./GatewayEdit";
import { pinRunAsValue, pinWorkflowValue, useAgentOptions, useSystemUserOptions, useWorkflowOptions } from "./hooks";
import { StringListField } from "./StringListField";

interface GatewayFormFieldProps {
    name: string;
    label: string;
    type: "string" | "number" | "password" | "boolean" | "text" | "select" | "textarea" | "array" | "array<object>" | "event_rules" | "agent_select" | "workflow_select" | "system_user_select" | "radio";
    register: UseFormRegister<FieldValues>;
    control?: Control<FieldValues>;
    error?: FieldError;
    required?: boolean;
    hint?: string;
    maxLength?: number;
    options?: { value: string; label: string }[];
    itemKey?: string;
    itemLabel?: string;
    placeholder?: string;
    fillWidth?: boolean;
    showTestConnection?: boolean;
    onTestConnection?: () => void;
    isTestingConnection?: boolean;
    showTemplateLookup?: boolean;
    onTemplateLookup?: () => void;
    connectionTestResult?: {
        success: boolean;
        message: string;
    } | null;
    onDismissConnectionTest?: () => void;
    isTestConnectionEnabled?: boolean;
    storedSecretNeedsReentry?: boolean;
}

function storedSecretHintText(needsReentry: boolean | undefined): string {
    if (needsReentry) return "Enter it again to use the changed connection settings.";
    return "Leave placeholder to keep the existing value.";
}

function heldTestTooltip(needsReentry: boolean | undefined, credentialTyped: boolean): string {
    if (needsReentry && !credentialTyped) return "Enter the credential again to test the changed connection settings";
    return "Required connection information is missing";
}

export const GatewayFormField = ({
    name,
    label,
    type,
    register,
    control,
    error,
    required,
    hint,
    maxLength,
    options,
    itemKey,
    itemLabel,
    placeholder,
    fillWidth,
    showTestConnection,
    onTestConnection,
    isTestingConnection,
    showTemplateLookup,
    onTemplateLookup,
    connectionTestResult,
    onDismissConnectionTest,
    isTestConnectionEnabled,
    storedSecretNeedsReentry,
}: GatewayFormFieldProps) => {
    const { control: formControl } = useFormContext();
    const fieldValue = useWatch({ control: formControl, name });
    const agentOptions = useAgentOptions();
    const { options: workflowOptions, isError: workflowsError } = useWorkflowOptions();
    const { options: systemUserOptions, isError: systemUsersError } = useSystemUserOptions();
    const storedSecrets = useStoredSecrets();

    const isTextareaType = type === "text" || type === "textarea";
    const shouldShowCharacterCount = isTextareaType && maxLength !== undefined;
    const selectWidthClass = fillWidth ? "w-full" : "w-full max-w-lg";

    const storedSecretHint = type === "password" && storedSecrets.has(name) ? storedSecretHintText(storedSecretNeedsReentry) : null;
    const fullHint = storedSecretHint ? [hint, storedSecretHint].filter(Boolean).join(" ") : hint;
    const effectiveHint = error?.message ? undefined : fullHint;

    const renderBoolean = (ctrl: Control<FieldValues>) => (
        <Controller
            name={name}
            control={ctrl}
            render={({ field }) => (
                <div className="flex items-center gap-2">
                    <Checkbox checked={field.value} onCheckedChange={field.onChange} />
                    <label onClick={() => field.onChange(!field.value)} className="cursor-pointer text-sm">
                        {label}
                    </label>
                </div>
            )}
        />
    );

    const renderSelect = (ctrl: Control<FieldValues>) => (
        <Controller
            name={name}
            control={ctrl}
            render={({ field }) => (
                <Select value={field.value || ""} onValueChange={field.onChange}>
                    <SelectTrigger invalid={!!error} className={selectWidthClass}>
                        <SelectValue placeholder="Select an option" />
                    </SelectTrigger>
                    <SelectContent>
                        {options?.map(opt => (
                            <SelectItem key={opt.value} value={opt.value}>
                                {opt.label}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
            )}
        />
    );

    const renderAgentSelect = (ctrl: Control<FieldValues>) => (
        <Controller
            name={name}
            control={ctrl}
            render={({ field }) => (
                <Select value={field.value || ""} onValueChange={field.onChange}>
                    <SelectTrigger aria-invalid={!!error} className={selectWidthClass}>
                        <SelectValue placeholder="Select an agent" />
                    </SelectTrigger>
                    <SelectContent>
                        {agentOptions.map(agent => (
                            <SelectItem key={agent.name} value={agent.name} className="w-lg">
                                {agent.displayName}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
            )}
        />
    );

    const renderWorkflowSelect = (ctrl: Control<FieldValues>) => (
        <Controller
            name={name}
            control={ctrl}
            render={({ field }) => {
                const options = pinWorkflowValue(workflowOptions, field.value);
                return (
                    <div className="flex flex-col gap-1">
                        <Select value={field.value || ""} onValueChange={field.onChange}>
                            <SelectTrigger aria-invalid={!!error} className={selectWidthClass}>
                                <SelectValue placeholder="Select a workflow" />
                            </SelectTrigger>
                            <SelectContent>
                                {options.map(workflow => (
                                    <SelectItem key={workflow.name} value={workflow.name} className="w-lg">
                                        {workflow.displayName}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                        {workflowsError && <div className="text-xs text-(--error-wMain)">Couldn&apos;t load workflows — the list may be incomplete. Any configured value is still shown above.</div>}
                    </div>
                );
            }}
        />
    );

    const renderSystemUserSelect = (ctrl: Control<FieldValues>) => (
        <Controller
            name={name}
            control={ctrl}
            render={({ field }) => {
                const options = pinRunAsValue(systemUserOptions, field.value);
                return (
                    <div className="flex flex-col gap-1">
                        <Select value={field.value || ""} onValueChange={field.onChange}>
                            <SelectTrigger aria-invalid={!!error} className={selectWidthClass}>
                                <SelectValue placeholder="Select a service account" />
                            </SelectTrigger>
                            <SelectContent>
                                {options.map(user => (
                                    <SelectItem key={user.name} value={user.name} className="w-lg">
                                        {user.displayName}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                        {systemUsersError && <div className="text-xs text-(--error-wMain)">Couldn&apos;t load service accounts — you may lack permission to list them. Any configured value is still shown above.</div>}
                    </div>
                );
            }}
        />
    );

    const renderStringDefault = () => {
        if (!control) {
            return <Input {...register(name)} type="text" autoComplete="off" aria-invalid={!!error} />;
        }
        return <Controller name={name} control={control} render={({ field }) => <Input {...field} value={field.value ?? ""} type="text" autoComplete="off" aria-invalid={!!error} />} />;
    };

    const renderEventRules = (ctrl: Control<FieldValues>) => <Controller name={name} control={ctrl} render={({ field }) => <EventRulesList value={field.value ?? []} onChange={field.onChange} />} />;

    const renderStringList = (ctrl: Control<FieldValues>) => (
        <Controller name={name} control={ctrl} render={({ field }) => <StringListField value={field.value ?? []} onChange={field.onChange} itemKey={itemKey ?? "value"} itemLabel={itemLabel} placeholder={placeholder} />} />
    );

    const renderRadio = (ctrl: Control<FieldValues>) => <Controller name={name} control={ctrl} render={({ field }) => <RadioOptions options={options ?? []} value={field.value} onChange={field.onChange} />} />;

    const renderControlled = (ctrl: Control<FieldValues>) => {
        switch (type) {
            case "password":
                return <PasswordInput name={name} control={ctrl} hasStoredValue={storedSecrets.has(name)} rules={{ required }} invalid={!!error} />;
            case "boolean":
                return renderBoolean(ctrl);
            case "radio":
                return renderRadio(ctrl);
            case "select":
                return renderSelect(ctrl);
            case "event_rules":
                return renderEventRules(ctrl);
            case "array<object>":
                return renderStringList(ctrl);
            case "agent_select":
                return renderAgentSelect(ctrl);
            case "workflow_select":
                return renderWorkflowSelect(ctrl);
            case "system_user_select":
                return renderSystemUserSelect(ctrl);
            default:
                return null;
        }
    };

    const renderInput = () => {
        switch (type) {
            case "number":
                return <Input {...register(name, { valueAsNumber: true })} type="number" aria-invalid={!!error} />;
            case "text":
            case "textarea":
                return <Textarea {...register(name)} rows={5} maxLength={maxLength ? maxLength + 1 : undefined} aria-invalid={!!error} />;
            case "password":
            case "boolean":
            case "radio":
            case "select":
            case "event_rules":
            case "array<object>":
            case "agent_select":
            case "workflow_select":
            case "system_user_select":
                return control ? renderControlled(control) : null;
            default:
                return renderStringDefault();
        }
    };

    if (type === "boolean") {
        return (
            <div className="space-y-1">
                {renderInput()}
                {effectiveHint && <div className="text-xs text-(--secondary-text-wMain)">{effectiveHint}</div>}
            </div>
        );
    }

    if (type === "event_rules") {
        return (
            <div className="space-y-2">
                {renderInput()}
                {error?.message && <ErrorLabel message={error.message} />}
            </div>
        );
    }

    if (type === "radio") {
        return (
            <PageLabelWithValue>
                <PageLabel required={required}>{label}</PageLabel>
                {effectiveHint && <div className="text-xs text-(--secondary-text-wMain)">{effectiveHint}</div>}
                {renderInput()}
                {error?.message && <ErrorLabel message={error.message} />}
            </PageLabelWithValue>
        );
    }

    return (
        <PageLabelWithValue>
            <PageLabel required={required}>{label}</PageLabel>
            {showTemplateLookup && onTemplateLookup ? (
                <div className="flex items-center gap-4">
                    <div className="flex-1">{renderInput()}</div>
                    <Button type="button" variant="outline" size="sm" testid="openWhatsappTemplateLookup" onClick={onTemplateLookup}>
                        <Search className="h-4 w-4" />
                        Find Template
                    </Button>
                </div>
            ) : (
                renderInput()
            )}
            {effectiveHint && <div className="text-xs text-(--secondary-text-wMain)">{effectiveHint}</div>}
            {type === "password" && showTestConnection && (
                <div className="mt-2 space-y-2">
                    <div className="flex items-center gap-2">
                        <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            onClick={onTestConnection}
                            disabled={!isTestConnectionEnabled || isTestingConnection}
                            tooltip={isTestConnectionEnabled ? undefined : heldTestTooltip(storedSecretNeedsReentry, !!fieldValue)}
                        >
                            Test Connection
                        </Button>
                        {isTestingConnection && <Loader2 className="h-4 w-4 animate-spin text-[var(--info-wMain)]" />}
                    </div>
                    {connectionTestResult && <MessageBanner variant={connectionTestResult.success ? "success" : "error"} message={connectionTestResult.message} dismissible onDismiss={onDismissConnectionTest} />}
                </div>
            )}
            {error?.message && <ErrorLabel message={error.message} />}
            {!error && shouldShowCharacterCount && <CharacterCountLabel currentLength={fieldValue?.length} maxLength={maxLength} />}
        </PageLabelWithValue>
    );
};
