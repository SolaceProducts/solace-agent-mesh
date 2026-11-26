// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { SolaceIcon } from "@/common/SolaceIcon";

export function AgentMeshBrand() {
    return (
        <span className="flex items-center gap-2">
            <SolaceIcon className="h-5" />
            <span className="border-l border-(--secondary-w40) pl-2 text-sm font-semibold text-(--primary-text-wMain)">Agent Mesh</span>
        </span>
    );
}
