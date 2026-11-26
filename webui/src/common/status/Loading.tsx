// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Loader2 } from "lucide-react";

export const Loading: React.FC = () => {
    return (
        <div className="flex flex-1 items-center justify-center">
            <Loader2 className="size-8 animate-spin text-(--brand-wMain)" />
        </div>
    );
};
