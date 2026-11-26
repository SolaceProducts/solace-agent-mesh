// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { Eye, EyeOff } from "lucide-react";
import type { Control, FieldValues, Path } from "react-hook-form";
import { Controller } from "react-hook-form";

import { Button, Input } from "@/ui";

export interface PasswordInputProps<T extends FieldValues = FieldValues> {
    name: Path<T>;
    control: Control<T>;
    hasStoredValue?: boolean;
    placeholder?: string;
    disabled?: boolean;
    rules?: Record<string, unknown>;
    invalid?: boolean;
}

const STORED_VALUE_PLACEHOLDER = "\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022";

export const PasswordInput = <T extends FieldValues = FieldValues>({ name, control, hasStoredValue = false, placeholder, disabled = false, rules, invalid = false }: PasswordInputProps<T>) => {
    const [showPassword, setShowPassword] = useState(false);
    const [userHasTouched, setUserHasTouched] = useState(false);

    return (
        <Controller
            name={name}
            control={control}
            rules={rules}
            render={({ field }) => {
                const hasUserInput = !!field.value || userHasTouched;
                const isEyeFunctional = hasUserInput || !hasStoredValue;
                const effectivePlaceholder = hasStoredValue && !hasUserInput ? STORED_VALUE_PLACEHOLDER : placeholder;
                const inputType = showPassword && isEyeFunctional ? "text" : "password";

                return (
                    <div className="relative">
                        <Input
                            {...field}
                            value={field.value ?? ""}
                            type={inputType}
                            placeholder={effectivePlaceholder}
                            autoComplete="new-password"
                            disabled={disabled}
                            className={isEyeFunctional ? "pr-10" : undefined}
                            role="textbox"
                            aria-invalid={invalid}
                            onFocus={() => {
                                setUserHasTouched(true);
                            }}
                        />
                        {isEyeFunctional && (
                            <Button
                                type="button"
                                onClick={() => setShowPassword(prev => !prev)}
                                disabled={disabled}
                                variant="ghost"
                                size="sm"
                                className="pointer-events-auto absolute top-1/2 right-1 -translate-y-1/2"
                                title={showPassword ? "Hide password" : "Show password"}
                                aria-label={showPassword ? "Hide password" : "Show password"}
                            >
                                {showPassword ? <EyeOff size={18} /> : <Eye size={18} />}
                            </Button>
                        )}
                    </div>
                );
            }}
        />
    );
};
