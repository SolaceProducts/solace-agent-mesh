// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useWhenProviderReady } from "@openfeature/react-sdk";
import { Outlet } from "react-router-dom";

import { useUserContext } from "@/providers/contexts";

import { useFeedbackManagementEnabled } from "@/hooks";

import { FeedbackAccessDenied } from "./FeedbackAccessDenied";

export function FeedbackGate() {
    const flagsReady = useWhenProviderReady({ suspendUntilReady: false });
    const { capabilitiesLoaded } = useUserContext();
    const allowed = useFeedbackManagementEnabled();

    if (!flagsReady || !capabilitiesLoaded) return null;
    if (!allowed) return <FeedbackAccessDenied />;

    return <Outlet />;
}
