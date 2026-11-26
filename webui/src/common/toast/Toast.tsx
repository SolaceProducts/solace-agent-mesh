// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { AlertTriangle, CircleCheckBig } from "lucide-react";

import { Alert, AlertTitle } from "@/ui/alert";

import type { Notification } from "@/types";

export function Toast({ id, message, type }: Notification) {
    return (
        <div id={id} className="transform transition-all duration-200 ease-in-out">
            <Alert className="light:border-none max-w-2xl min-w-58 rounded-sm bg-(--darkSurface-bg) px-4 py-0 text-(--darkSurface-text) shadow-[0_4px_6px_-1px_var(--secondary-w8040)]">
                <AlertTitle className="flex items-center py-1.5">
                    {type === "warning" && <AlertTriangle className="mr-2 shrink-0 text-(--warning-wMain)" />}
                    {type === "success" && <CircleCheckBig className="mr-2 shrink-0 text-(--success-wMain)" />}
                    <div className="break-words">{message}</div>
                </AlertTitle>
            </Alert>
        </div>
    );
}
