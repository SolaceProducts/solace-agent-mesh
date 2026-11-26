// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { useSystemUsers } from "@/api/rbac";

export interface SystemUserOption {
    name: string;
    displayName: string;
}

export interface SystemUserOptions {
    options: SystemUserOption[];
    isError: boolean;
}

const EMPTY: never[] = [];

export const useSystemUserOptions = (): SystemUserOptions => {
    const { data: systemUsers = EMPTY, isError } = useSystemUsers();

    const options = useMemo(() => {
        return (systemUsers ?? EMPTY)
            .map(u => ({ name: u.name, displayName: u.displayName || u.name }))
            .sort((a, b) => {
                if (a.name === "default") return -1;
                if (b.name === "default") return 1;
                return a.displayName.localeCompare(b.displayName);
            });
    }, [systemUsers]);

    return { options, isError };
};

export const pinRunAsValue = (options: SystemUserOption[], value: string | undefined | null): SystemUserOption[] => {
    if (!value) return options;
    if (options.some(o => o.name === value)) return options;
    return [...options, { name: value, displayName: value }];
};
