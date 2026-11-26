// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useState } from "react";
import { Code, List, Plus, X } from "lucide-react";
import { useFormContext } from "react-hook-form";

import { PageLabel, PageLabelWithValue } from "@/common/layout";
import { ErrorLabel } from "@/common/status";

import { Button } from "@/ui/button";
import { Checkbox } from "@/ui/checkbox";
import { Input } from "@/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";
import { Textarea } from "@/ui/textarea";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";

import type { FieldProps } from "../ConnectorEdit";

const PARAM_TYPES = [
    { value: "string", label: "String" },
    { value: "integer", label: "Integer" },
    { value: "number", label: "Number" },
    { value: "boolean", label: "Boolean" },
] as const;

interface ParameterEditable {
    _uid: string;
    name: string;
    type: string;
    required: boolean;
    description: string;
    payload_path: string;
}

interface Parameter extends ParameterEditable {
    [key: string]: unknown;
}

const newUid = () => (typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : `p-${Math.random().toString(36).slice(2)}-${Date.now()}`);

const makeDefaultParameter = (): Parameter => ({
    _uid: newUid(),
    name: "",
    type: "string",
    required: false,
    description: "",
    payload_path: "",
});

export function ConnectorParametersField({ fieldPath, fieldConfig, fieldError }: Readonly<FieldProps>) {
    const { setValue, watch } = useFormContext();
    const [isJsonMode, setIsJsonMode] = useState(false);

    const rawJsonValue = watch(fieldPath) ?? "[]";

    const parseParameters = useCallback((jsonStr: string): Parameter[] => {
        try {
            const parsed = JSON.parse(jsonStr || "[]");
            if (!Array.isArray(parsed)) return [];
            return parsed.map((p: any) => ({
                ...p,
                _uid: newUid(),
                name: p.name ?? "",
                type: p.type ?? "string",
                required: p.required ?? false,
                description: p.description ?? "",
                payload_path: p.payload_path ?? "",
            }));
        } catch {
            return [];
        }
    }, []);

    const serializeParameters = useCallback((params: Parameter[]): string => {
        const cleaned = params
            .filter(p => p.name.trim() !== "")
            .map(p => {
                const { _uid: _, name, type, required, description, payload_path, ...extraKeys } = p;
                const param: Record<string, unknown> = { ...extraKeys, name, type };
                if (required) param.required = true;
                if (description) param.description = description;
                if (payload_path) param.payload_path = payload_path;
                return param;
            });
        return cleaned.length > 0 ? JSON.stringify(cleaned) : "";
    }, []);

    const [parameters, setParameters] = useState<Parameter[]>(() => parseParameters(rawJsonValue));

    const updateFormValue = useCallback(
        (params: Parameter[]) => {
            setParameters(params);
            setValue(fieldPath, serializeParameters(params), { shouldValidate: true });
        },
        [fieldPath, setValue, serializeParameters]
    );

    const handleAddParameter = useCallback(() => {
        updateFormValue([...parameters, makeDefaultParameter()]);
    }, [parameters, updateFormValue]);

    const handleRemoveParameter = useCallback(
        (index: number) => {
            const updated = parameters.filter((_, i) => i !== index);
            updateFormValue(updated);
        },
        [parameters, updateFormValue]
    );

    const handleUpdateParameter = useCallback(
        (index: number, field: keyof Parameter, value: any) => {
            const updated = parameters.map((p, i) => (i === index ? { ...p, [field]: value } : p));
            updateFormValue(updated);
        },
        [parameters, updateFormValue]
    );

    const handleToggleMode = useCallback(() => {
        if (isJsonMode) {
            setParameters(parseParameters(rawJsonValue));
        }
        setIsJsonMode(!isJsonMode);
    }, [isJsonMode, rawJsonValue, parseParameters]);

    const handleJsonChange = useCallback(
        (e: React.ChangeEvent<HTMLTextAreaElement>) => {
            setValue(fieldPath, e.target.value, { shouldValidate: true });
        },
        [fieldPath, setValue]
    );

    return (
        <PageLabelWithValue>
            <div className="flex items-center justify-between">
                <div>
                    <PageLabel>{fieldConfig.label}</PageLabel>
                    {fieldConfig.hint && <div className="text-sm text-(--secondary-text-wMain)">{fieldConfig.hint}</div>}
                </div>
                <div className="flex items-center gap-2">
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <Button type="button" variant="ghost" size="sm" onClick={handleToggleMode}>
                                {isJsonMode ? <List className="mr-2 size-4" /> : <Code className="mr-2 size-4" />}
                                {isJsonMode ? "Visual Builder" : "JSON"}
                            </Button>
                        </TooltipTrigger>
                        <TooltipContent>{isJsonMode ? "Switch to visual builder" : "Switch to JSON editor"}</TooltipContent>
                    </Tooltip>
                    {!isJsonMode && (
                        <Button type="button" variant="ghost" size="sm" onClick={handleAddParameter}>
                            <Plus className="mr-2 size-4" />
                            Add Parameter
                        </Button>
                    )}
                </div>
            </div>

            {isJsonMode ? (
                <div className="mt-2">
                    <Textarea
                        value={rawJsonValue}
                        onChange={handleJsonChange}
                        className="resize-none font-mono text-sm"
                        rows={8}
                        placeholder='[{"name": "order_id", "type": "string", "required": true, "description": "Order ID", "payload_path": "request.order_id"}]'
                    />
                </div>
            ) : (
                <div className="mt-2 space-y-3">
                    {parameters.length === 0 ? (
                        <div className="rounded-md border border-dashed border-(--border-wMain) p-4 text-center text-sm text-(--secondary-text-wMain)">No parameters defined. Click "Add Parameter" to add one.</div>
                    ) : (
                        parameters.map((param, index) => (
                            <div key={param._uid} className="rounded-md border border-(--border-wMain) p-3">
                                <div className="mb-2 flex items-center justify-between">
                                    <span className="text-sm font-medium">Parameter {index + 1}</span>
                                    <Button type="button" variant="ghost" size="sm" onClick={() => handleRemoveParameter(index)}>
                                        <X className="size-4" />
                                    </Button>
                                </div>
                                <div className="grid grid-cols-2 gap-3">
                                    <div>
                                        <label htmlFor={`param-name-${index}`} className="mb-1 block text-xs text-(--secondary-text-wMain)">
                                            Name *
                                        </label>
                                        <Input id={`param-name-${index}`} value={param.name} onChange={e => handleUpdateParameter(index, "name", e.target.value)} placeholder="order_id" className="h-8 text-sm" />
                                    </div>
                                    <div>
                                        <label htmlFor={`param-type-${index}`} className="mb-1 block text-xs text-(--secondary-text-wMain)">
                                            Type
                                        </label>
                                        <Select value={param.type} onValueChange={value => handleUpdateParameter(index, "type", value)}>
                                            <SelectTrigger id={`param-type-${index}`} className="h-8 text-sm">
                                                <SelectValue />
                                            </SelectTrigger>
                                            <SelectContent>
                                                {PARAM_TYPES.map(t => (
                                                    <SelectItem key={t.value} value={t.value}>
                                                        {t.label}
                                                    </SelectItem>
                                                ))}
                                            </SelectContent>
                                        </Select>
                                    </div>
                                    <div className="col-span-2">
                                        <label htmlFor={`param-desc-${index}`} className="mb-1 block text-xs text-(--secondary-text-wMain)">
                                            Description
                                        </label>
                                        <Input id={`param-desc-${index}`} value={param.description} onChange={e => handleUpdateParameter(index, "description", e.target.value)} placeholder="The order ID to look up" className="h-8 text-sm" />
                                    </div>
                                    <div>
                                        <label htmlFor={`param-path-${index}`} className="mb-1 block text-xs text-(--secondary-text-wMain)">
                                            Payload Path
                                        </label>
                                        <Input id={`param-path-${index}`} value={param.payload_path} onChange={e => handleUpdateParameter(index, "payload_path", e.target.value)} placeholder="request.order_id" className="h-8 text-sm" />
                                    </div>
                                    <div className="flex items-end pb-1">
                                        <label className="flex cursor-pointer items-center gap-2 text-sm">
                                            <Checkbox checked={param.required} onCheckedChange={checked => handleUpdateParameter(index, "required", checked === true)} />
                                            Required
                                        </label>
                                    </div>
                                </div>
                            </div>
                        ))
                    )}
                </div>
            )}

            {fieldError && <ErrorLabel message={fieldError.message} />}
        </PageLabelWithValue>
    );
}
