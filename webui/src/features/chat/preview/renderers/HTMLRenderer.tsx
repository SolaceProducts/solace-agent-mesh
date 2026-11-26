// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import type { BaseRendererProps } from ".";

const SANDBOX_WITHOUT_SAME_ORIGIN = "allow-scripts allow-downloads allow-forms allow-popups";

export const HtmlRenderer: React.FC<BaseRendererProps> = ({ content, setRenderError }) => {
    return (
        <div className="h-full w-full overflow-hidden border">
            <iframe
                srcDoc={content}
                title="HTML Preview"
                sandbox={SANDBOX_WITHOUT_SAME_ORIGIN}
                allow="autoplay"
                className="h-full w-full border-none bg-white"
                onError={() => setRenderError("Failed to load HTML content.")}
                onLoad={() => setRenderError(null)}
            />
        </div>
    );
};
