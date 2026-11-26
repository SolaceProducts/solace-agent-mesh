// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { X } from "lucide-react";

import { Button } from "@/ui/button";
import { Checkbox } from "@/ui/checkbox";

import type { CardGrant, CardGrants } from "./bundles";

export interface GrantCardProps {
    label: string;
    description?: string;
    allOf?: { noun: "agent" | "workflow" };
    showTraces: boolean;
    grants: CardGrants;
    onGrantChange: (grant: CardGrant, on: boolean) => void;
    onRemove: () => void;
    disabled?: boolean;
}

function GrantRow({ label, ariaLabel, note, checked, disabled, onChange }: Readonly<{ label: string; ariaLabel: string; note?: string; checked: boolean; disabled: boolean; onChange?: (on: boolean) => void }>) {
    return (
        <label className={`flex w-fit items-center gap-2 ${disabled ? "cursor-not-allowed opacity-50" : "cursor-pointer"}`}>
            <Checkbox aria-label={ariaLabel} checked={checked} disabled={disabled} onCheckedChange={onChange} />
            <span className="text-sm">
                {label}
                {note && <span className="ml-1 text-(--secondary-text-wMain)">{note}</span>}
            </span>
        </label>
    );
}

export function GrantCard({ label, description, allOf, showTraces, grants, onGrantChange, onRemove, disabled = false }: Readonly<GrantCardProps>) {
    const subtext = allOf ? `Every ${allOf.noun}, including ones created later.` : description;

    return (
        <li className="flex flex-col gap-3 rounded-xs border px-3 py-2">
            <div className="flex items-start justify-between gap-2">
                <span className="flex min-w-0 flex-1 flex-col gap-1">
                    <span className="truncate" title={label}>
                        {label}
                    </span>
                    {subtext && (
                        <span className="truncate text-xs text-(--secondary-text-wMain)" title={subtext}>
                            {subtext}
                        </span>
                    )}
                </span>
                {!disabled && (
                    <Button variant="ghost" size="icon" tooltip="Remove" onClick={onRemove}>
                        <X />
                    </Button>
                )}
            </div>
            <div className="flex flex-col gap-2">
                <GrantRow label="Can use" ariaLabel={`${label}: Can use`} note={disabled ? undefined : "— always granted"} checked={disabled ? grants.invoke : true} disabled />
                <GrantRow label="Can view feedback" ariaLabel={`${label}: Can view feedback`} checked={grants.feedbackRead} disabled={disabled} onChange={on => onGrantChange("feedbackRead", on)} />
                {showTraces && (
                    <GrantRow
                        label="Can view activities"
                        ariaLabel={`${label}: Can view activities`}
                        note="— all agent activities and full execution content"
                        checked={grants.traceRead}
                        disabled={disabled}
                        onChange={on => onGrantChange("traceRead", on)}
                    />
                )}
            </div>
        </li>
    );
}
