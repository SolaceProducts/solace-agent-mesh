// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useState } from "react";

export const LogoImage: React.FC<{ url: string; className: string; fallback: React.ReactNode }> = ({ url, className, fallback }) => {
    const [errored, setErrored] = useState(false);
    if (errored) return <>{fallback}</>;
    return (
        <img
            src={url}
            alt="Logo"
            className={className}
            onError={() => {
                console.error("custom logo failed to load", { url });
                setErrored(true);
            }}
        />
    );
};
