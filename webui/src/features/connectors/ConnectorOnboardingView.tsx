// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useUserContext } from "@/providers/contexts";

import { Button } from "@/ui/button";

import ConnectorEmptyState from "./assets/connectorsEmptyStateLight.svg";
import { ConnectorsAvailableDialog } from "./ConnectorsAvailableDialog";

export const ConnectorOnboardingView = ({ title, description }: { title: string; description: string }) => {
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const canCreateConnectors = userHasCapability(CAPABILITIES.createConnectors);
    return (
        <div className="flex h-full items-center justify-center">
            <div className="flex max-w-2xl flex-col items-center gap-6 px-6 text-center">
                <div className="mb-4 flex max-w-md">
                    <img src={ConnectorEmptyState} alt="Connectors" />
                </div>
                <h2 className="text-xl font-semibold">{title}</h2>
                <p className="text-(--secondary-text-wMain)">{description}</p>
                {canCreateConnectors && <ConnectorsAvailableDialog triggerButton={<Button variant="default">See Available Connectors</Button>} />}
            </div>
        </div>
    );
};
