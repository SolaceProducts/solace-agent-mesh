// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { useConfigContext } from "@/providers/contexts";

import { messageColourVariants } from "@/common";

export const PlatformConfigIndicator = () => {
    const { platformConfigured } = useConfigContext();

    const shouldShow = useMemo(() => {
        return !platformConfigured;
    }, [platformConfigured]);

    return (
        <div
            className="overflow-hidden transition-all duration-300 ease-in-out"
            style={{
                maxHeight: shouldShow ? "120px" : "0px",
                opacity: shouldShow ? 1 : 0,
            }}
        >
            <div className={`flex w-full items-center justify-center px-3 py-1.5 text-center text-sm sm:h-8 sm:py-0 ${messageColourVariants.warning}`}>
                Platform Service is not configured. Contact your administrator to enable connector management.
            </div>
        </div>
    );
};
