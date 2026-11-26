// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { PageLabel } from "@/common/layout";

interface AgentCommunicationModeProps {
    label: string;
    modes?: string[];
}

export const AgentCommunicationMode = ({ label, modes }: AgentCommunicationModeProps) => {
    return (
        <>
            <PageLabel>{label}</PageLabel>
            <div className="flex flex-wrap items-center gap-2">
                {modes && modes.length > 0 ? (
                    modes.map((mode, index) => (
                        <div key={index} className="rounded-sm bg-(--secondary-w10) px-3 py-1 text-xs">
                            {mode}
                        </div>
                    ))
                ) : (
                    <div className="text-xs text-(--secondary-text-wMain)">None</div>
                )}
            </div>
        </>
    );
};
