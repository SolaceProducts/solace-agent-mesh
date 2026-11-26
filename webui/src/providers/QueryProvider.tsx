// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { type QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { queryClient } from "@/providers/query/QueryClient";

export const QueryProvider: React.FC<{ children: React.ReactNode; client?: QueryClient }> = ({ children, client = queryClient }) => {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
};
