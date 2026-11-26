// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { cn } from "@/utils";

interface SwitchProps {
    checked?: boolean;
    onCheckedChange?: (checked: boolean) => void;
    disabled?: boolean;
    className?: string;
    id?: string;
    "aria-label"?: string;
    "aria-labelledby"?: string;
    "aria-describedby"?: string;
}

function Switch({ checked = false, onCheckedChange, disabled = false, className, id, "aria-label": ariaLabel, "aria-labelledby": ariaLabelledby, "aria-describedby": ariaDescribedby }: Readonly<SwitchProps>) {
    const handleClick = () => {
        if (!disabled && onCheckedChange) {
            onCheckedChange(!checked);
        }
    };

    return (
        <button
            type="button"
            role="switch"
            id={id}
            aria-checked={checked}
            aria-label={ariaLabel}
            aria-labelledby={ariaLabelledby}
            aria-describedby={ariaDescribedby}
            disabled={disabled}
            onClick={handleClick}
            className={cn(
                "peer inline-flex h-6 w-11 shrink-0 cursor-pointer items-center rounded-full border-2 border-transparent transition-colors focus-visible:ring-2 focus-visible:ring-(--brand-wMain) focus-visible:ring-offset-2 focus-visible:ring-offset-(--background-w10) focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50",
                checked ? "bg-(--primary-wMain)" : "bg-(--primary-w40)",
                className
            )}
        >
            <span className={cn("pointer-events-none block h-5 w-5 rounded-full bg-(--background-w10) shadow-lg ring-0 transition-transform", checked ? "translate-x-5" : "translate-x-0")} />
        </button>
    );
}

export { Switch };
