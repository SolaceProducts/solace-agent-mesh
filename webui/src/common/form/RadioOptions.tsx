// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useId } from "react";

import { cn } from "@/utils";

export interface RadioOption {
    value: string;
    label: string;
    description?: string;
    badge?: ReactNode;
}

export const RadioOptions = ({
    options,
    value,
    onChange,
    disabled,
    orientation = "horizontal",
    deselectable = false,
    ariaLabel,
}: {
    options: readonly RadioOption[];
    value: string;
    onChange: (value: string) => void;
    disabled?: boolean;
    orientation?: "horizontal" | "vertical";
    deselectable?: boolean;
    ariaLabel?: string;
}) => {
    const groupName = useId();
    const vertical = orientation === "vertical";
    const cursor = disabled ? "cursor-not-allowed" : "cursor-pointer";
    return (
        <div role="radiogroup" aria-label={ariaLabel} className={vertical ? "flex flex-col gap-3" : "flex gap-4"}>
            {options.map(option => {
                const selected = value === option.value;
                const labelId = `${groupName}-${option.value}-label`;
                const descriptionId = option.description ? `${groupName}-${option.value}-description` : undefined;
                const input = (
                    <input
                        type="radio"
                        name={groupName}
                        value={option.value}
                        checked={selected}
                        onChange={() => onChange(option.value)}
                        onClick={deselectable && selected ? () => onChange("") : undefined}
                        disabled={disabled}
                        aria-labelledby={labelId}
                        aria-describedby={descriptionId}
                        className={cn(cursor, !vertical && option.description && "mt-1")}
                    />
                );
                const horizontalAlign = option.description ? "items-start" : "items-center";
                const labelClass = vertical ? `flex ${cursor} items-baseline gap-3` : `flex ${cursor} gap-2 ${horizontalAlign}`;
                return (
                    <label key={option.value} className={labelClass}>
                        {vertical ? <span className="flex h-6 w-6 shrink-0 items-center justify-center self-start">{input}</span> : input}
                        <span className="flex flex-col gap-1">
                            <span className="flex items-center gap-2">
                                <span id={labelId}>{option.label}</span>
                                {option.badge}
                            </span>
                            {option.description && (
                                <span id={descriptionId} className="text-(--secondary-text-wMain)">
                                    {option.description}
                                </span>
                            )}
                        </span>
                    </label>
                );
            })}
        </div>
    );
};
