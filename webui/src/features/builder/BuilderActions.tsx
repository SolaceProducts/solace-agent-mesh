// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";
import { Rocket } from "lucide-react";

import { useChatContext } from "@/providers/contexts";

import { useIsBuilderServerApplyEnabled } from "@/hooks/useIsBuilderServerApplyEnabled";

import { Button } from "@/ui/button";

import { buildBlockedByCredsMessage, type ConfigType, deployableSessionComponents, isBlockedSessionComponent } from "./builderUtils";
import { useArtifactVersions } from "./hooks/useArtifactVersions";
import type { BuildManifest, SaveManifestResult } from "./hooks/useBuilderManifest";
import { useManifestDeployment } from "./hooks/useManifestDeployment";
import { useRecordSavedComponents } from "./hooks/useRecordSavedComponents";
import { deployAfterSave, type DeployFailureState, type DeployManifestOptions, useSaveOrchestration } from "./hooks/useSaveOrchestration";
import { IDLE_PLATFORM_CHECK, type PlatformCheck } from "./platformCheck";
import type { FetchConfigByFilename } from "./serverApply";

export interface BuildActivateApi {
    canBuildActivate: boolean;
    platformCheck: PlatformCheck;
    checkAgain: () => void;
    canAskBuilder: boolean;
    askBuilder: () => void;
    turnRunning: boolean;
    isBusy: boolean;
    run: () => Promise<void>;
    failure: DeployFailureState;
    retry: () => void;
}

interface BuilderActionsProps {
    configYaml: string;
    configType: ConfigType;
    manifest?: BuildManifest | null;
    componentsMissingCreds?: ReadonlyMap<string, number>;
    saveManifest?: (updater: (prev: BuildManifest) => BuildManifest) => Promise<SaveManifestResult>;
    fetchConfigByFilename?: FetchConfigByFilename;
    userAgentsRefetch?: () => Promise<void>;
    yamlAgentCardsRefetch?: () => void;
    autoSave?: boolean;
    onAutoSaveComplete?: () => void;
    onBuildActivateApiChange?: (api: BuildActivateApi | null) => void;
}

function isBuildDeployed(manifest: BuildManifest | null | undefined): boolean {
    const deployable = deployableSessionComponents(manifest?.components);
    return deployable.length > 0 && deployable.every(c => c.status === "deployed");
}

function isNeutralNote(failure: DeployFailureState): boolean {
    return failure.kind === "fixing" || failure.kind === "declined";
}

export function BuilderActions({
    configYaml,
    configType,
    manifest,
    componentsMissingCreds,
    saveManifest,
    fetchConfigByFilename,
    userAgentsRefetch,
    yamlAgentCardsRefetch,
    autoSave,
    onAutoSaveComplete,
    onBuildActivateApiChange,
}: Readonly<BuilderActionsProps>) {
    const { displayError } = useChatContext();
    const serverApplyEnabled = useIsBuilderServerApplyEnabled();
    const bridged = !!onBuildActivateApiChange;
    const handleDeployRef = useRef<(() => Promise<void>) | null>(null);
    const handleDeployManifestRef = useRef<((changedOnPlatform?: readonly string[], options?: DeployManifestOptions) => Promise<void>) | null>(null);
    const autoDeployTriggeredRef = useRef(false);
    const manualDeployRef = useRef(false);

    const recordSavedComponents = useRecordSavedComponents(saveManifest);
    const artifactVersions = useArtifactVersions(manifest);

    const [buildToActivateSeen, setBuildToActivateSeen] = useState(false);

    const save = useSaveOrchestration({
        hasBuildToActivate: buildToActivateSeen,
        configYaml,
        configType,
        manifest,
        fetchConfigByFilename,
        userAgentsRefetch,
        autoSave,
        onAutoSaveComplete,
        handleDeployRef,
        handleDeployManifestRef,
        autoDeployTriggeredRef,
        bridged,
        recordSavedComponents,
        artifactVersions,
    });

    const deploy = useManifestDeployment({
        manifest,
        componentsMissingCreds,
        saveManifest: saveManifest ?? (async () => ({ ok: true }) as SaveManifestResult),
        savedComponents: save.savedComponents,
        changedOnPlatformRef: save.changedOnPlatformRef,
        matchedOnPlatformRef: save.matchedOnPlatformRef,
        savedResultsRef: save.savedResultsRef,
        declinedOnPlatformRef: save.declinedOnPlatformRef,
        allSaved: save.allSaved,
        autoSavedInThisFlowRef: save.autoSavedInThisFlowRef,
        userAgentsRefetch,
        yamlAgentCardsRefetch,
        reportErrorToAgent: save.reportErrorToAgent,
        setError: save.setError,
        setBlockedError: save.setBlockedError,
        setDeployOutcomeError: save.setDeployOutcomeError,
        clearUnlessDeployOutcome: save.clearUnlessDeployOutcome,
        autoDeployTriggeredRef,
        manualDeployRef,
        bridged,
    });

    handleDeployRef.current = deploy.handleDeploy;
    handleDeployManifestRef.current = deploy.handleDeployManifest;

    let buildActivateLabel = "Build & Activate";
    if (save.isSaving) {
        buildActivateLabel = "Saving…";
    } else if (deploy.isDeployingManifest || deploy.isDeploying) {
        buildActivateLabel = "Deploying…";
    }

    const autoSaveOwnsFlow = !!autoSave && save.readyToSave;
    const hasBlockedComponent = (manifest?.components ?? []).some(isBlockedSessionComponent);
    const buildToActivate = !!(deploy.hasUndeployedSessionComponents && !autoSaveOwnsFlow && save.validationStamp.state === "current" && !hasBlockedComponent);
    useEffect(() => setBuildToActivateSeen(buildToActivate), [buildToActivate]);
    const platformCheck = serverApplyEnabled && buildToActivate ? save.platformCheck : IDLE_PLATFORM_CHECK;
    const canBuildActivate = buildToActivate && (!serverApplyEnabled || save.platformCheck.kind === "passed");
    const isBusy = save.isSaving || deploy.isDeploying || deploy.isDeployingManifest;

    const buildAlreadyDeployed = isBuildDeployed(manifest);
    const { endApproval } = save;
    useEffect(() => {
        if (buildAlreadyDeployed) endApproval();
    }, [buildAlreadyDeployed, endApproval]);

    const runBuildActivate = useCallback(async () => {
        if (save.isSavingRef.current) return;
        endApproval();
        if (componentsMissingCreds && componentsMissingCreds.size > 0) {
            displayError({ title: "Credentials required", error: buildBlockedByCredsMessage(componentsMissingCreds), errorSource: "client" });
            return;
        }
        manualDeployRef.current = true;
        autoDeployTriggeredRef.current = true;
        const saved = await save.handleSaveAll();
        const next = deployAfterSave(saved);
        if (!next.deploy) return;
        await deploy.handleDeployManifest(next.changedOnPlatform, next.options);
    }, [componentsMissingCreds, displayError, save.handleSaveAll, save.isSavingRef, deploy.handleDeployManifest, endApproval]);

    useEffect(() => {
        onBuildActivateApiChange?.({
            canBuildActivate,
            platformCheck,
            checkAgain: save.checkAgain,
            canAskBuilder: save.canAskBuilder,
            askBuilder: save.askBuilder,
            turnRunning: save.turnRunning,
            isBusy,
            run: runBuildActivate,
            failure: save.deployFailure,
            retry: save.handleManualRetry,
        });
    }, [onBuildActivateApiChange, canBuildActivate, platformCheck, save.checkAgain, save.canAskBuilder, save.askBuilder, save.turnRunning, isBusy, runBuildActivate, save.deployFailure, save.handleManualRetry]);
    useEffect(() => {
        return () => onBuildActivateApiChange?.(null);
    }, [onBuildActivateApiChange]);

    return (
        <>
            {!bridged && canBuildActivate && (
                <Button size="sm" className="gap-1.5 text-xs" onClick={runBuildActivate} disabled={isBusy}>
                    <Rocket className="h-3 w-3" />
                    {buildActivateLabel}
                </Button>
            )}

            {!bridged && save.deployFailure.kind !== "none" && (
                <div className="flex items-center gap-2">
                    <span className={`max-w-[400px] truncate text-xs ${isNeutralNote(save.deployFailure) ? "text-(--secondary-text-wMain)" : "text-(--error-wMain)"}`} title={save.deployFailure.message}>
                        {save.deployFailure.kind === "fixing" ? "⟳ Sent to builder to fix — retrying shortly…" : save.deployFailure.message}
                    </span>
                    {(save.deployFailure.kind === "retryable" || save.deployFailure.kind === "declined") && !save.isSaving && !deploy.isDeploying && (
                        <Button variant="ghost" size="sm" onClick={save.handleManualRetry} className="h-auto px-0 py-0 text-xs font-medium text-(--accent-n2-wMain) hover:underline">
                            Retry
                        </Button>
                    )}
                </div>
            )}

            {save.connectorEditDialog}
            {save.overwriteDialog}
        </>
    );
}
