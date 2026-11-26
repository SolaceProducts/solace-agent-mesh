// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import type { BaseRendererProps } from ".";

export const ImageRenderer: React.FC<BaseRendererProps> = ({ content, mimeType, setRenderError }) => {
    const imageUrl = `data:${mimeType || "image/png"};base64,${content}`;

    return (
        <div className="flex h-auto w-auto max-w-[100vw] items-center justify-center p-4">
            <img src={imageUrl} onError={() => setRenderError("Failed to load image")} onLoad={() => setRenderError(null)} alt="Preview" className="h-full w-full object-contain" />
        </div>
    );
};
