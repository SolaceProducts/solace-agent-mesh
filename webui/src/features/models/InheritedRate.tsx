// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { HelpPopover } from "@/common";

export function InheritedRate({ label, amount }: Readonly<{ label: string; amount: string }>) {
    return (
        <span className="inline-flex items-center gap-1">
            {amount}
            <span className="sr-only"> (defaults to the input price)</span>
            <HelpPopover label={`${label} price help`} content={`The ${label.toLowerCase()} price isn't set, so it defaults to the input price.`} size="16" />
        </span>
    );
}
