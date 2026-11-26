// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

export const PageSection = ({ children, className = "" }: { children: React.ReactNode; className?: string }) => {
    return <div className={`flex flex-col gap-2 ${className}`}>{children}</div>;
};
