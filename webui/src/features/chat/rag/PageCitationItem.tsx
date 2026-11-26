// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { Button } from "@/ui/button";

export interface LocationCitationItemProps {
    locationLabel: string;
    citationCount: number;
    onView?: () => void;
}

const getViewButtonText = (locationLabel: string): string => {
    if (locationLabel.startsWith("Page")) return "View Page";
    if (locationLabel.startsWith("Lines ")) return "View Lines";
    if (locationLabel.startsWith("Slide")) return "View Slide";
    if (locationLabel.startsWith("Paragraph")) return "View Paragraph";
    return "View";
};

export const LocationCitationItem: React.FC<LocationCitationItemProps> = ({ locationLabel, citationCount, onView }) => {
    const buttonText = getViewButtonText(locationLabel);

    return (
        <div className="flex items-center justify-between py-2.5">
            <div className="flex items-center gap-2">
                <span className="min-w-[60px] text-sm font-semibold">{locationLabel}</span>
                <span className="text-sm">
                    {citationCount} citation{citationCount !== 1 ? "s" : ""}
                </span>
            </div>
            {onView && (
                <Button variant="link" className="h-auto p-0 text-sm" onClick={onView}>
                    {buttonText}
                </Button>
            )}
        </div>
    );
};
