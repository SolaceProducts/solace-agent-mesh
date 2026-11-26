// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

export const Deprecated = ({ children }: { children: React.ReactNode }) => {
    return <span className="inline-flex items-center rounded-full bg-(--warning-w10) px-2 py-1 text-xs font-medium text-(--warning-w100)">{children}</span>;
};
