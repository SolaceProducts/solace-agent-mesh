// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { LucideIcon } from "lucide-react";

export function ActivityEmptyState({ message, icon: Icon, children }: { message: string; icon: LucideIcon; children?: React.ReactNode }) {
    return (
        <div className="flex h-full items-center justify-center p-4">
            <div className="text-center">
                <Icon className="mx-auto mb-3 h-10 w-10" />
                <p className="text-sm">{message}</p>
                {children}
            </div>
        </div>
    );
}
