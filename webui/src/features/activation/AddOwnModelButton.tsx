// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

import { useChatContext } from "@/providers/contexts";

import { ModelSetupDialog } from "@/common/dialogs/ModelSetupDialog";

import { Button } from "@/ui";

import { OWN_MODEL_SETUP_COPY } from "./ownModelSetup";
import { useFreeModel } from "./useFreeModel";

export function AddOwnModelButton({ variant = "default", size, dense, className }: Readonly<{ variant?: "default" | "outline" | "ghost" | "link"; size?: "default" | "sm"; dense?: boolean; className?: string }>) {
    const { hasModelConfigWrite } = useChatContext();
    const { isFreeModel } = useFreeModel();
    const [open, setOpen] = useState(false);
    return (
        <>
            <Button variant={variant} size={size} dense={dense} className={className} onClick={() => setOpen(true)}>
                Replace With My Own Model
            </Button>
            <ModelSetupDialog open={open} onOpenChange={setOpen} hasWritePermissions={hasModelConfigWrite} setupCopy={OWN_MODEL_SETUP_COPY} replaces={isFreeModel} />
        </>
    );
}
