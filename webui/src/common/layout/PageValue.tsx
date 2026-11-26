// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

export const PageValue = ({ children, className = "" }: { children: React.ReactNode; className?: string }) => {
    return <div className={`text-sm ${className}`}>{children}</div>;
};
