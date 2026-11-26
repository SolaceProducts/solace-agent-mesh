// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useNavigate } from "react-router-dom";

import { useModelConfigStatus } from "@/api/models";

import { useChatContext } from "@/providers/contexts";

import { Button } from "@/ui";

import { MessageBanner } from "./MessageBanner";

export function ModelWarningBanner() {
    const navigate = useNavigate();
    const { data: modelConfigStatus } = useModelConfigStatus();
    const { hasModelConfigWrite } = useChatContext();

    const showWarning = modelConfigStatus && !modelConfigStatus.configured;

    if (!showWarning) return null;

    return (
        <MessageBanner
            variant="warning"
            style={{ alignItems: "center" }}
            message={
                <div className="flex w-full items-center justify-between gap-3">
                    <span>
                        No default model has been set up. Some features may not work as intended without a configured model.
                        {hasModelConfigWrite ? " Go to Agent Mesh to configure your models." : " Ask your administrator to configure models in Agent Mesh."}
                    </span>
                    {hasModelConfigWrite && (
                        <Button variant="outline" size="sm" className="shrink-0" onClick={() => navigate("/models")}>
                            Set Up Default Models
                        </Button>
                    )}
                </div>
            }
        />
    );
}
