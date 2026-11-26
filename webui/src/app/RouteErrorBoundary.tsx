// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect } from "react";
import { useNavigate, useRouteError } from "react-router-dom";

import ErrorState from "./ErrorState";

function RouteErrorBoundary() {
    const error = useRouteError();
    const navigate = useNavigate();

    useEffect(() => {
        console.error("Route render error:", error);
    }, [error]);

    return <ErrorState message="This page ran into an unexpected error. Reload to try again." actions={[{ kind: "button", label: "Reload Page", onClick: () => navigate(0) }]} />;
}

export default RouteErrorBoundary;
