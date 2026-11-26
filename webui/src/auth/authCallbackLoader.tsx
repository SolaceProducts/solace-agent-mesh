// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createRoot } from "react-dom/client";

import { installStaleChunkRecovery } from "@/utils/staleChunkRecovery";

import AuthCallback from "./authCallback";

installStaleChunkRecovery();

createRoot(document.getElementById("root")!).render(<AuthCallback />);
