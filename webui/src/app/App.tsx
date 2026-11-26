// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useWhenProviderReady } from "@openfeature/react-sdk";
import { RouterProvider } from "react-router-dom";

import { useUserContext } from "@/providers/contexts";

import { ProductAnalyticsGate } from "@/common/product-analytics";

import { ActivationGate } from "@/features/activation";

import { EmptyState } from "@/common";
import { useRestoreWheelDispatch } from "@/hooks";
import { AudioSettingsProvider, AuthProvider, ConfigProvider, CsrfProvider, FeatureFlagProvider, ProjectProvider, QueryProvider, SSEProvider, TaskProvider, ThemeProvider, UIModeProvider, UserProvider, ZoomProvider } from "@/providers";

import ErrorBoundary from "./ErrorBoundary";
import { useCreateRouter } from "./router";

function AppRouter() {
    const router = useCreateRouter();
    const { capabilitiesLoaded } = useUserContext();
    const flagsReady = useWhenProviderReady();

    if (!capabilitiesLoaded || !flagsReady) {
        return (
            <div className="flex h-screen w-screen items-center justify-center">
                <EmptyState variant="loading" title="Loading..." />
            </div>
        );
    }
    return <RouterProvider router={router} />;
}

function App() {
    useRestoreWheelDispatch();

    return (
        <ErrorBoundary>
            <QueryProvider>
                <ThemeProvider>
                    <ZoomProvider>
                        <CsrfProvider>
                            <ConfigProvider>
                                <UIModeProvider>
                                    <AuthProvider>
                                        {__SAM_PRODUCT_ANALYTICS__ && <ProductAnalyticsGate />}
                                        <ActivationGate>
                                            <FeatureFlagProvider>
                                                <SSEProvider>
                                                    <UserProvider>
                                                        <ProjectProvider>
                                                            <AudioSettingsProvider>
                                                                <TaskProvider>
                                                                    <AppRouter />
                                                                </TaskProvider>
                                                            </AudioSettingsProvider>
                                                        </ProjectProvider>
                                                    </UserProvider>
                                                </SSEProvider>
                                            </FeatureFlagProvider>
                                        </ActivationGate>
                                    </AuthProvider>
                                </UIModeProvider>
                            </ConfigProvider>
                        </CsrfProvider>
                    </ZoomProvider>
                </ThemeProvider>
            </QueryProvider>
        </ErrorBoundary>
    );
}

export default App;
