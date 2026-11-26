// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Ellipsis } from "lucide-react";

import { Button } from "@/ui/button";

export const MorePopoverButton = (
    <Button variant="ghost" tooltip="More">
        <Ellipsis className="h-5 w-5" />
    </Button>
);
