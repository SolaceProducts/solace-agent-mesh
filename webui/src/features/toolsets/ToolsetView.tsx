// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { PAGE_COMMON_CLASSES, PageSection } from "@/common/layout";

import { cn } from "@/utils/cnTailwind";

import { MessageBanner, Metadata } from "@/common";
import type { Toolset } from "@/types";

import { ToolsetDiscoveryIndicator } from "./ToolsetDiscoveryIndicator";
import { ToolsetDisplayField } from "./ToolsetDisplayField";
import { ToolsetToolCards } from "./ToolsetToolCards";

interface ToolsetViewProps {
    toolset: Toolset;
}

export const ToolsetView: React.FC<ToolsetViewProps> = ({ toolset }) => {
    const showDiscoveryErrors = (toolset.discoveryStatus === "failed" || toolset.discoveryStatus === "removed") && toolset.discoveryErrors && toolset.discoveryErrors.length > 0;

    return (
        <div className="flex-1 overflow-y-auto p-8 leading-5">
            <div className={cn("flex flex-col gap-8", PAGE_COMMON_CLASSES)}>
                {showDiscoveryErrors && (
                    <MessageBanner
                        variant="error"
                        message={
                            <div className="flex flex-col gap-1">
                                <span>Discovery Errors</span>
                                <ul className="flex flex-col gap-0.5 font-normal">
                                    {toolset.discoveryErrors!.map(err => (
                                        <li key={err} className="font-mono text-xs">
                                            {err}
                                        </li>
                                    ))}
                                </ul>
                            </div>
                        }
                    />
                )}

                <PageSection className="gap-6">
                    <ToolsetDisplayField label="Name" value={toolset.name} />
                    <ToolsetDisplayField label="Description" value={toolset.description} fieldType="textarea" className="break-all" />
                    <div className="grid grid-cols-2 gap-4">
                        {toolset.discoveryStatus && (
                            <div>
                                <div className="mb-2 text-sm font-medium text-(--secondary-text-wMain)">Status</div>
                                <ToolsetDiscoveryIndicator toolset={toolset} showReady />
                            </div>
                        )}
                    </div>
                </PageSection>

                <PageSection>
                    <div className="mb-2 text-(--secondary-text-wMain)">Tools</div>
                    <ToolsetToolCards files={toolset.files ?? []} discoveryStatus={toolset.discoveryStatus} />
                </PageSection>

                {toolset?.id ? <Metadata metadata={toolset} /> : null}
            </div>
        </div>
    );
};
