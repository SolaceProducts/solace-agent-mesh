// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useMemo } from "react";

import { useChatContext } from "@/providers/contexts";

import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";

import type { ArtifactInfo } from "@/types";

interface ArtifactVersionNavigationProps {
    artifactInfo: ArtifactInfo;
}

export const ArtifactVersionNavigation: React.FC<ArtifactVersionNavigationProps> = ({ artifactInfo }) => {
    const { previewedArtifactAvailableVersions, currentPreviewedVersionNumber, navigateArtifactVersion } = useChatContext();
    const versions = useMemo(() => previewedArtifactAvailableVersions ?? [], [previewedArtifactAvailableVersions]);

    return (
        <>
            {versions.length > 1 ? (
                <div className="align-right">
                    <Select
                        value={currentPreviewedVersionNumber?.toString()}
                        onValueChange={value => {
                            navigateArtifactVersion(artifactInfo.filename, parseInt(value));
                        }}
                    >
                        <SelectTrigger className="h-[16px] py-0 text-xs shadow-none">
                            <SelectValue placeholder="Version" />
                        </SelectTrigger>
                        <SelectContent>
                            {versions.map(version => (
                                <SelectItem key={version} value={version.toString()}>
                                    Version {version}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                </div>
            ) : null}
        </>
    );
};
