// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { Check } from "lucide-react";

interface CheckboxProps {
    checked?: boolean;
    onCheckedChange?: (checked: boolean) => void;
    disabled?: boolean;
    className?: string;
    id?: string;
    "aria-label"?: string;
    "aria-describedby"?: string;
    "aria-invalid"?: boolean;
    "aria-required"?: boolean;
}

export const Checkbox: React.FC<CheckboxProps> = ({
    checked = false,
    onCheckedChange,
    disabled = false,
    className = "",
    id,
    "aria-label": ariaLabel,
    "aria-describedby": ariaDescribedBy,
    "aria-invalid": ariaInvalid,
    "aria-required": ariaRequired,
}) => {
    return (
        <span className={`relative inline-flex h-5 w-5 shrink-0 ${className} `}>
            <input
                type="checkbox"
                id={id}
                aria-label={ariaLabel}
                aria-describedby={ariaDescribedBy}
                aria-invalid={ariaInvalid}
                aria-required={ariaRequired}
                checked={checked}
                disabled={disabled}
                onChange={event => onCheckedChange?.(event.target.checked)}
                className={`h-5 w-5 appearance-none rounded border-2 transition-colors ${
                    checked ? "border-(--primary-wMain) bg-(--primary-wMain) text-(--primary-text-w10)" : "bg-(--background-w10) hover:border-(--primary-wMain)"
                } ${disabled ? "cursor-not-allowed opacity-50" : "cursor-pointer"}`}
            />
            {checked && <Check className="pointer-events-none absolute inset-0 m-auto h-3 w-3 text-(--primary-text-w10)" />}
        </span>
    );
};
