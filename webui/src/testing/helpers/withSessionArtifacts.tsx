// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Decorator } from "@storybook/react-vite";

import type { ArtifactInfo } from "@/types";

import { SeedSessionArtifacts } from "./sessionArtifacts";

export function withSessionArtifacts(sessionId: string, artifacts: ArtifactInfo[]): Decorator {
    return Story => (
        <>
            <SeedSessionArtifacts sessionId={sessionId} artifacts={artifacts} />
            <Story />
        </>
    );
}
