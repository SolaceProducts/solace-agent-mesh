// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { AddOwnModelButton } from "./AddOwnModelButton";

export function FreeModelManagedNotice() {
    return (
        <section aria-labelledby="free-model-managed-title" className="flex max-w-2xl flex-col items-start gap-3">
            <h2 id="free-model-managed-title" className="text-base font-semibold text-(--primary-text-wMain)">
                This model is managed by Solace Agent Mesh
            </h2>
            <p className="text-sm text-(--secondary-text-wMain)">This model runs on free credits, so it cannot be edited or deleted. To use a different provider, add a model. The default models switch to the new model.</p>
            <AddOwnModelButton />
        </section>
    );
}
