// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { PoweredByClaude } from "./PoweredByClaude";
import type { FreeModelLabel } from "./useFreeModel";

export function FreeModelName({ label }: Readonly<{ label: FreeModelLabel }>) {
    return (
        <span className="flex items-center gap-2">
            {label.modelName}
            <PoweredByClaude pill />
        </span>
    );
}
