// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import "./index.css";

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import App from "./app/App.tsx";
import { unregisterStaleServiceWorkers } from "./unregisterStaleServiceWorkers";
import { installStaleChunkRecovery } from "./utils/staleChunkRecovery";
import { startTeamsHandshakeIfPossible } from "./utils/teamsAuth";

void unregisterStaleServiceWorkers();
installStaleChunkRecovery();

startTeamsHandshakeIfPossible();

createRoot(document.getElementById("root")!).render(
    <StrictMode>
        <App />
    </StrictMode>
);
