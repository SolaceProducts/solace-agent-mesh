// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UsageKind, UsageRow, UsageUser } from "@/api/usage";

import { Button } from "@/ui/button";
import { TruncatedText } from "@/ui/truncated-text";

import { cn } from "@/utils";

import { detailTargetFor, type UsageDetailTarget } from "./usageDetailTarget";
import { displayName, isUnknownUser } from "./usageKinds";

interface SubjectNameProps {
    kind: UsageKind;
    row: UsageRow & { user?: UsageUser | null };
    onOpen?: (target: UsageDetailTarget) => void;
    className?: string;
}

export function SubjectName({ kind, row, onOpen, className }: Readonly<SubjectNameProps>) {
    const name = displayName(kind, row);
    const unknown = isUnknownUser(kind, row);
    const target = detailTargetFor(kind, row);
    if (!target || !onOpen) {
        return <TruncatedText text={name} className={cn(unknown ? "text-(--secondary-text-wMain)" : ["font-medium", target === null && "text-(--secondary-text-wMain)"], className)} />;
    }
    return (
        <Button
            variant="link"
            size="sm"
            truncate
            className={cn("h-auto min-w-0 px-0", unknown ? "font-normal text-(--secondary-text-wMain)" : "font-medium text-(--primary-text-wMain) enabled:hover:no-underline", className)}
            onClick={() => onOpen(target)}
        >
            <TruncatedText text={name} className="block" />
        </Button>
    );
}
