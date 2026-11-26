// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { lazy, Suspense, useEffect } from "react";
import { X } from "lucide-react";

import { Button } from "@/ui";

import { ActivationBackdrop } from "./ActivationBackdrop";
import { AgentMeshBrand } from "./AgentMeshBrand";
import { useModelSetupRequest } from "./ModelSetupRequest";

const ModelSetupForm = lazy(() => import("@/common/dialogs/ModelSetupForm"));

interface WelcomeSetupPageProps {
    onDone: () => void;
}

export function WelcomeSetupPage({ onDone }: Readonly<WelcomeSetupPageProps>) {
    return (
        <div className="fixed inset-0 z-50">
            <ActivationBackdrop wide>
                <dialog open aria-modal="true" aria-labelledby="welcome-setup-title" className="static flex max-h-none w-full max-w-none flex-col gap-6 rounded-lg bg-(--background-w10) p-6 text-inherit shadow-xl">
                    <div className="flex items-center justify-between">
                        <AgentMeshBrand />
                        <Button variant="ghost" size="icon" onClick={onDone} aria-label="Close">
                            <X />
                        </Button>
                    </div>
                    <div className="flex flex-col gap-2">
                        <h1 id="welcome-setup-title" className="text-xl font-semibold text-(--primary-text-wMain)">
                            Welcome to Solace Agent Mesh
                        </h1>
                        <p className="text-sm text-(--secondary-text-wMain)">
                            Connect your own model provider and you're ready to go. You can set or change these values later in <strong>Models</strong> under the Builder section.
                        </p>
                    </div>
                    <Suspense fallback={null}>
                        <ModelSetupForm onSuccess={onDone} onClose={onDone} hideCancel submitLabel="Complete Setup" />
                        <ReportShown />
                    </Suspense>
                </dialog>
            </ActivationBackdrop>
        </div>
    );
}

function ReportShown() {
    const { shown } = useModelSetupRequest();
    useEffect(shown, [shown]);
    return null;
}
