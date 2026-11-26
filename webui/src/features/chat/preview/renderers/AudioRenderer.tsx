// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import type { BaseRendererProps } from ".";

export const AudioRenderer: React.FC<BaseRendererProps> = ({ content, mimeType, setRenderError }) => {
    const audioSrc = `data:${mimeType || "audio/mpeg"};base64,${content}`;

    return (
        <div className="flex h-auto max-w-[100vw] items-center justify-center p-4">
            <audio controls className="w-full" onLoad={() => setRenderError(null)} onError={() => setRenderError("Failed to load audio content.")}>
                <source src={audioSrc} type={mimeType || "audio/mpeg"} />
                Your browser does not support the audio element.
            </audio>
        </div>
    );
};
