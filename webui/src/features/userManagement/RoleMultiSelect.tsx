// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";
import { XIcon } from "lucide-react";

import { MultiSelect } from "@/common/MultiSelect";

import { Badge } from "@/ui/badge";

import type { RBACRole } from "@/types";

interface RoleMultiSelectProps {
    roles: readonly RBACRole[];
    value: string[];
    onChange: (roleIds: string[]) => void;
    disabled?: boolean;
}

export function RoleMultiSelect({ roles, value, onChange, disabled }: Readonly<RoleMultiSelectProps>) {
    const options = useMemo(() => roles.map(r => r.name), [roles]);
    const nameById = useMemo(() => new Map(roles.map(r => [r.id, r.name])), [roles]);
    const idByName = useMemo(() => new Map(roles.map(r => [r.name, r.id])), [roles]);

    const selectedNames = value.map(id => nameById.get(id)).filter((n): n is string => Boolean(n));
    const handleChange = (names: string[]) => onChange(names.map(name => idByName.get(name)).filter((id): id is string => Boolean(id)));

    return (
        <MultiSelect
            options={options}
            value={selectedNames}
            onChange={handleChange}
            disabled={disabled}
            renderChip={(name, remove) => (
                <Badge className="max-w-50 gap-1 py-0.5 pr-1">
                    <span className="min-w-0 truncate" title={name}>
                        {name}
                    </span>
                    {!disabled && (
                        <button type="button" aria-label={`Remove ${name}`} onClick={remove} className="grid size-4 shrink-0 place-items-center rounded-sm hover:bg-(--secondary-w10)">
                            <XIcon className="size-3" aria-hidden />
                        </button>
                    )}
                </Badge>
            )}
        />
    );
}
