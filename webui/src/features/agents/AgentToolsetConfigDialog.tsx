// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useMemo, useState } from "react";
import { Eye, EyeOff, Settings } from "lucide-react";

import { CustomCheckbox } from "@/common/CustomCheckbox";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";

import type { ConfigSchemaField } from "@/types";
import { REDACTED_SENTINEL } from "@/types";

const PasswordField: React.FC<{
    value: string;
    onChange: (v: string) => void;
    placeholder?: string;
}> = ({ value, onChange, placeholder }) => {
    const [show, setShow] = useState(false);
    return (
        <div className="relative flex items-center">
            <input
                type={show ? "text" : "password"}
                value={value}
                placeholder={placeholder}
                onChange={e => onChange(e.target.value)}
                className="w-full rounded-sm border px-3 py-1.5 pr-10 text-sm focus:ring-1 focus:ring-(--primary-wMain) focus:outline-none"
            />
            <button
                type="button"
                aria-pressed={show}
                aria-label={show ? "Hide password" : "Show password"}
                onClick={() => setShow(s => !s)}
                className="absolute right-2 flex items-center text-(--secondary-text-wMain) hover:text-(--primary-wMain)"
                tabIndex={0}
            >
                {show ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
            </button>
        </div>
    );
};

const SchemaFormField: React.FC<{
    field: ConfigSchemaField;
    value: unknown;
    onChange: (v: unknown) => void;
    error?: string;
    coveredByToolsetNote?: string;
}> = ({ field, value, onChange, error, coveredByToolsetNote }) => {
    const strValue = value !== undefined && value !== null ? String(value) : "";
    const isRedacted = field.secret && strValue === REDACTED_SENTINEL;

    const testid = `schemaField-${field.key}`;
    const renderInput = () => {
        if (field.type === "boolean") {
            return <CustomCheckbox checked={!!value} onChange={() => onChange(!value)} />;
        }
        if (field.type === "integer") {
            return (
                <input
                    data-testid={testid}
                    type="number"
                    step="1"
                    value={strValue}
                    onChange={e => onChange(e.target.value === "" ? "" : parseInt(e.target.value, 10))}
                    className="w-full rounded-sm border px-3 py-1.5 text-sm focus:ring-1 focus:ring-(--primary-wMain) focus:outline-none"
                />
            );
        }
        if (field.secret) {
            return <PasswordField value={strValue} onChange={v => onChange(v)} placeholder={isRedacted ? "(unchanged — clear to replace)" : undefined} />;
        }
        return <input data-testid={testid} type="text" value={strValue} onChange={e => onChange(e.target.value)} className="w-full rounded-sm border px-3 py-1.5 text-sm focus:ring-1 focus:ring-(--primary-wMain) focus:outline-none" />;
    };

    return (
        <div className="flex flex-col gap-1">
            <label className="text-sm font-medium text-(--secondary-text-wMain)">
                {field.key}
                {field.required && <span className="ml-1 text-(--accent-n2-wMain)">*</span>}
                {coveredByToolsetNote && <span className="ml-2 font-normal text-(--secondary-text-wMain)">{coveredByToolsetNote}</span>}
            </label>
            {renderInput()}
            {field.description && <p className="text-xs text-(--secondary-text-wMain)">{field.description}</p>}
            {error && <p className="text-sm text-(--error-wMain)">{error}</p>}
        </div>
    );
};

interface AgentToolsetConfigDialogProps {
    toolName: string;
    configSchema: ConfigSchemaField[];
    configValues: Record<string, unknown>;
    onSave: (toolName: string, values: Record<string, unknown>) => void;
    readOnly?: boolean;
    isToolsetDefault?: boolean;
}

export function AgentToolsetConfigDialog({ toolName, configSchema, configValues, onSave, readOnly = false, isToolsetDefault = false }: AgentToolsetConfigDialogProps) {
    const [isOpen, setIsOpen] = useState(false);
    const [localValues, setLocalValues] = useState<Record<string, unknown>>({});
    const [errors, setErrors] = useState<Record<string, string>>({});

    const storedSecrets = useMemo(() => {
        const secrets = new Set<string>();
        for (const field of configSchema) {
            if (field.secret) {
                const val = configValues[field.key];
                if (val !== undefined && val !== null && val !== "") {
                    secrets.add(field.key);
                }
            }
        }
        return secrets;
    }, [configSchema, configValues]);

    const handleOpen = useCallback(() => {
        const initial: Record<string, unknown> = {};
        for (const field of configSchema) {
            if (field.secret && storedSecrets.has(field.key)) {
                continue;
            }
            if (field.key in configValues) {
                initial[field.key] = configValues[field.key];
            } else if (field.default !== undefined) {
                initial[field.key] = field.default;
            }
        }
        setLocalValues(initial);
        setErrors({});
        setIsOpen(true);
    }, [configSchema, configValues, storedSecrets]);

    const handleFieldChange = useCallback((key: string, value: unknown) => {
        setLocalValues(prev => ({ ...prev, [key]: value }));
        setErrors(prev => {
            if (!prev[key]) return prev;
            const next = { ...prev };
            delete next[key];
            return next;
        });
    }, []);

    const validate = useCallback((): boolean => {
        const newErrors: Record<string, string> = {};
        for (const field of configSchema) {
            if (field.required) {
                const hasStoredSecret = storedSecrets.has(field.key);
                const value = localValues[field.key];
                if ((value === undefined || value === "") && !hasStoredSecret) {
                    newErrors[field.key] = `${field.key} is required`;
                }
            }
        }
        setErrors(newErrors);
        return Object.keys(newErrors).length === 0;
    }, [configSchema, storedSecrets, localValues]);

    const handleSave = useCallback(() => {
        if (!validate()) return;

        const cleaned: Record<string, unknown> = {};
        for (const [key, value] of Object.entries(localValues)) {
            const hasStoredSecret = storedSecrets.has(key);
            if (value !== "" && value !== undefined) {
                cleaned[key] = value;
            } else if (hasStoredSecret) {
                cleaned[key] = "";
            }
        }
        onSave(toolName, cleaned);
        setIsOpen(false);
    }, [localValues, onSave, toolName, validate, storedSecrets]);

    const description = isToolsetDefault
        ? "Set default parameters for this tool. Values are inherited by every agent that uses this toolset and control how the tool behaves."
        : "Set parameters for this tool. Values are unique to this agent and control how the tool behaves.";

    return (
        <>
            <Button type="button" variant="ghost" size="sm" onClick={handleOpen} title="Configure" className="h-6 w-6 p-0">
                <Settings className="size-4" />
            </Button>

            <Dialog open={isOpen} onOpenChange={setIsOpen}>
                <DialogContent className="flex max-h-[85vh] flex-col overflow-hidden sm:max-w-2xl">
                    <DialogHeader>
                        <DialogTitle>Tool Configuration</DialogTitle>
                    </DialogHeader>

                    <div className="flex flex-col gap-4 overflow-y-auto px-0.5 pt-4 pb-2">
                        <div className="text-sm">{description}</div>

                        <div className="space-y-4">
                            {configSchema.map(field => (
                                <SchemaFormField
                                    key={field.key}
                                    field={field}
                                    value={localValues[field.key]}
                                    onChange={v => handleFieldChange(field.key, v)}
                                    error={errors[field.key]}
                                    coveredByToolsetNote={storedSecrets.has(field.key) ? "(stored)" : undefined}
                                />
                            ))}
                        </div>
                    </div>

                    <DialogFooter>
                        {readOnly ? (
                            <Button type="button" onClick={() => setIsOpen(false)}>
                                Close
                            </Button>
                        ) : (
                            <>
                                <Button type="button" variant="ghost" onClick={() => setIsOpen(false)}>
                                    Cancel
                                </Button>
                                <Button type="button" onClick={handleSave}>
                                    Save Configuration
                                </Button>
                            </>
                        )}
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </>
    );
}
