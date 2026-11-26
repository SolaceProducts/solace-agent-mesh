// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import { AlertTriangle, ChevronDown, ChevronRight, CircleCheckBig, Loader2, Pencil, Play, RefreshCw, Rocket, Variable } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";
import { Checkbox } from "@/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";
import { Textarea } from "@/ui/textarea";

import { AddCommentMenu, calculateMenuPosition } from "@/features/chat/selection";

import { MarkdownWrapper, MessageBanner } from "@/common";

import { blockedReasonOf } from "./blockedComponents";
import { componentTypeLabel, humanizeComponentName, isBlockedSessionComponent } from "./builderUtils";
import { componentTypeIcon, StatusBadge, typeColor } from "./canvas/CardShell";
import { lookupConnectorIconByKey } from "./canvas/connectorIconInference";
import type { BuildActivateFailure } from "./hooks/useBuildActivateBridge";
import type { BuildManifest, SaveManifestResult } from "./hooks/useBuilderManifest";
import { awaitsActivation, type PlatformCheckView } from "./platformCheck";
import { PlatformCheckStatus } from "./PlatformCheckStatus";
import { declinedLeavesButton } from "./saveOnlyPolicy";

interface InlinePlanCardProps {
    manifest: BuildManifest;
    onApprove: () => void;
    onRequestChanges: (feedback: string) => void;
    onSaveManifest?: (updater: (prev: BuildManifest) => BuildManifest) => Promise<SaveManifestResult>;
    isApproved: boolean;
    isResponding?: boolean;
    isCollapsed?: boolean;
    onCollapsedChange?: (collapsed: boolean) => void;
    canBuildActivate?: boolean;
    platformCheck?: PlatformCheckView;
    onCheckAgain?: () => void;
    onAskBuilder?: () => void;
    isBuildActivating?: boolean;
    onBuildActivate?: () => void;
    deployFailure?: BuildActivateFailure | null;
    onRetry?: () => void;
    onResumeBuild?: () => void;
    droppedNotice?: ReactNode;
}

type PlanComponent = NonNullable<BuildManifest["components"]>[number];

const COMPONENT_TYPE_ORDER: Record<string, number> = {
    gateway: 0,
    workflow: 1,
    agent: 2,
    skill: 3,
    connector: 4,
    toolset: 5,
};

function sortComponents(components: NonNullable<BuildManifest["components"]>) {
    return [...components].sort((a, b) => (COMPONENT_TYPE_ORDER[a.type] ?? 99) - (COMPONENT_TYPE_ORDER[b.type] ?? 99));
}

const DEPLOY_LIVE_TYPES = new Set(["agent", "workflow", "gateway", "a2a_agent"]);

function joinList(items: string[]): string {
    if (items.length <= 1) return items[0] ?? "";
    if (items.length === 2) return `${items[0]} and ${items[1]}`;
    return `${items.slice(0, -1).join(", ")}, and ${items.at(-1)}`;
}

function buildActivateCopy(allComponents: NonNullable<BuildManifest["components"]>): string {
    if (allComponents.some(isBlockedSessionComponent)) {
        return "This will generate every component that can be created now. Nothing is deployed until the component marked as needing input has what it needs.";
    }
    const deployLiveCopy = "This will generate all component configurations and deploy them live. Your agents, workflows, and entrypoints will be active immediately.";
    const components = allComponents.filter(c => c.origin !== "platform");
    if (components.length === 0) return deployLiveCopy;

    const attachOnly = [...new Set(components.filter(c => !DEPLOY_LIVE_TYPES.has(c.type)).map(c => c.type))].sort((a, b) => a.localeCompare(b));
    const hasDeployLive = components.some(c => DEPLOY_LIVE_TYPES.has(c.type));
    if (attachOnly.length === 0) return deployLiveCopy;

    const one = attachOnly.length === 1;
    const nouns = one ? attachOnly[0] : joinList(attachOnly.map(t => `${t}s`));
    if (!hasDeployLive) {
        return one
            ? `This will generate your ${nouns} configuration. It won't run on its own — attach it to an agent afterward to use it.`
            : `This will generate configurations for your ${nouns}. They won't run on their own — attach them to an agent afterward to use them.`;
    }
    return `This will generate all component configurations. Your agents, workflows, and entrypoints deploy live immediately; your ${nouns} must be attached to an agent to take effect.`;
}

const SKIP_CONFIRM_KEY = "sam_builder_skip_activate_confirm";

function shouldSkipActivateConfirm(): boolean {
    try {
        return localStorage.getItem(SKIP_CONFIRM_KEY) === "true";
    } catch {
        return false;
    }
}

function persistSkipActivateConfirm(): void {
    try {
        localStorage.setItem(SKIP_CONFIRM_KEY, "true");
    } catch {}
}

function InterruptedBuildNotice({ show, onResume }: Readonly<{ show: boolean; onResume?: () => void }>) {
    if (!show || !onResume) return null;
    return (
        <>
            <div className="px-4 pt-2.5">
                <MessageBanner variant="warning" message="This build was interrupted before it finished. Resume it to pick up where it stopped." />
            </div>
            <div className="flex flex-col gap-2 px-4 py-2.5 sm:flex-row sm:items-center sm:justify-end">
                <Button size="sm" onClick={onResume} className="gap-1.5">
                    <Play className="h-3.5 w-3.5" />
                    Resume build
                </Button>
            </div>
        </>
    );
}

function planDisplayName(manifest: BuildManifest): string {
    return manifest.display_name || manifest.name || "Untitled Build";
}

function EnvVarsSection({ envVars, open, onToggle }: Readonly<{ envVars: NonNullable<BuildManifest["env_vars"]>; open: boolean; onToggle: () => void }>) {
    if (envVars.length === 0) return null;
    return (
        <div className="px-4 py-3">
            <Button variant="ghost" size="sm" onClick={onToggle} className="flex h-auto w-full items-center justify-start gap-1.5 px-0 py-0 text-xs font-medium">
                {open ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
                <Variable className="h-3 w-3" />
                Environment Variables ({envVars.length})
            </Button>
            {open && (
                <div className="mt-1.5 space-y-1 pl-5">
                    {envVars.map(v => (
                        <div key={v.name} className="flex items-center gap-2">
                            <code className="text-xs font-medium">{v.name}</code>
                            {v.required && <span className="text-xs font-medium text-(--error-wMain)">required</span>}
                        </div>
                    ))}
                </div>
            )}
        </div>
    );
}

function allSessionComponentsBuilt(components: readonly PlanComponent[]): boolean {
    const sessionComps = components.filter(c => c.origin !== "platform");
    return sessionComps.length > 0 && sessionComps.every(c => c.status === "validated" || c.status === "deployed");
}

function FailureAndAction({ failure, declined, canRetry, text, actionButton }: Readonly<{ failure: BuildActivateFailure | null | undefined; declined: boolean; canRetry: boolean; text: string; actionButton: React.ReactNode }>) {
    const fixing = !!failure && !canRetry;
    return (
        <>
            {failure && (
                <div className="px-4 pt-2.5">
                    <MessageBanner variant={declined ? "info" : "error"} message={<span title={declined ? undefined : failure.message}>{text}</span>} />
                </div>
            )}
            {(actionButton || fixing) && (
                <div className="flex flex-col gap-2 px-4 py-2.5 sm:flex-row sm:items-center sm:justify-end">
                    {fixing ? (
                        <span className="inline-flex items-center gap-1.5 text-xs text-(--secondary-text-wMain)">
                            <Loader2 className="h-3.5 w-3.5 animate-spin" />
                            The builder is fixing this — retrying shortly…
                        </span>
                    ) : (
                        actionButton
                    )}
                </div>
            )}
        </>
    );
}

function failureView(reported: BuildActivateFailure | null | undefined, card: { isApproved?: boolean; hasBuildToActivate: boolean; isResponding?: boolean; hasHandler: boolean }) {
    const buildActivateOnScreen = !!card.isApproved && card.hasBuildToActivate && !card.isResponding && card.hasHandler;
    const deployFailure = declinedLeavesButton(!!reported?.declined, buildActivateOnScreen) ? null : reported;
    const canRetry = !!deployFailure?.canRetry;
    const declined = !!deployFailure?.declined;
    let deployFailureText = "This build couldn’t be fully created and activated — the builder is retrying.";
    if (declined) deployFailureText = deployFailure?.message ?? "";
    else if (canRetry) deployFailureText = deployFailure?.message ? `This build couldn’t be completed: ${deployFailure.message}` : "This build couldn’t be fully created and activated. Try again to finish setting it up.";
    return { buildActivateOnScreen, deployFailure, canRetry, declined, deployFailureText };
}

export function InlinePlanCard({
    manifest,
    onApprove,
    onRequestChanges,
    onSaveManifest,
    isApproved,
    isResponding,
    isCollapsed: controlledCollapsed,
    onCollapsedChange,
    canBuildActivate,
    isBuildActivating,
    onBuildActivate,
    platformCheck,
    onCheckAgain,
    onAskBuilder,
    deployFailure: reportedFailure,
    onRetry,
    onResumeBuild,
    droppedNotice,
}: Readonly<InlinePlanCardProps>) {
    const [showArchitecture, setShowArchitecture] = useState(false);
    const [archEditMode, setArchEditMode] = useState(false);
    const [archEditText, setArchEditText] = useState("");
    const [archSaving, setArchSaving] = useState(false);
    const [archError, setArchError] = useState<string | null>(null);
    const [showEnvVars, setShowEnvVars] = useState(false);
    const [feedbackMode, setFeedbackMode] = useState(false);
    const [feedbackText, setFeedbackText] = useState("");
    const feedbackRef = useRef<HTMLTextAreaElement>(null);
    const [showConfirmDialog, setShowConfirmDialog] = useState(false);
    const [dontShowAgain, setDontShowAgain] = useState(false);
    const [commentMenu, setCommentMenu] = useState<{ comp: PlanComponent; position: { x: number; y: number } } | null>(null);

    const [internalCollapsed, setInternalCollapsed] = useState(false);
    const isCollapsed = controlledCollapsed ?? internalCollapsed;
    const setIsCollapsed = useCallback(
        (value: boolean) => {
            onCollapsedChange ? onCollapsedChange(value) : setInternalCollapsed(value);
        },
        [onCollapsedChange]
    );

    const hasBuildToActivate = awaitsActivation(canBuildActivate, platformCheck);

    const hasAutoCollapsed = useRef(false);
    useEffect(() => {
        if (!isApproved || hasAutoCollapsed.current || hasBuildToActivate) return;
        hasAutoCollapsed.current = true;
        const timer = setTimeout(() => setIsCollapsed(true), 800);
        return () => clearTimeout(timer);
    }, [isApproved, hasBuildToActivate, setIsCollapsed]);

    const pendingActivateRef = useRef<(() => void) | null>(null);
    const requestActivate = useCallback((action: () => void) => {
        if (shouldSkipActivateConfirm()) {
            action();
        } else {
            pendingActivateRef.current = action;
            setShowConfirmDialog(true);
        }
    }, []);
    const dismissConfirmDialog = useCallback(() => {
        pendingActivateRef.current = null;
        setShowConfirmDialog(false);
    }, []);

    const { buildActivateOnScreen, deployFailure, canRetry, declined, deployFailureText } = failureView(reportedFailure, { isApproved, hasBuildToActivate, isResponding, hasHandler: !!onBuildActivate });
    const showRetry = !!deployFailure && canRetry && !!onRetry;
    const showBuildActivate = buildActivateOnScreen && !deployFailure;
    const retryButton =
        showRetry && onRetry ? (
            <Button size="sm" onClick={onRetry} disabled={isBuildActivating} className="gap-1.5">
                <RefreshCw className="h-3.5 w-3.5" />
                Try again
            </Button>
        ) : null;
    const buildActivateButton =
        showBuildActivate && onBuildActivate ? (
            <Button size="sm" onClick={() => requestActivate(onBuildActivate)} disabled={isBuildActivating || !canBuildActivate} className="gap-1.5">
                <Rocket className="h-3.5 w-3.5" />
                {isBuildActivating ? "Activating…" : "Build & Activate"}
            </Button>
        ) : null;
    const actionButton = retryButton ?? buildActivateButton;

    const displayName = planDisplayName(manifest);
    const components = sortComponents(manifest.components ?? []);
    const envVars = manifest.env_vars ?? [];

    const hasChangedComponents = components.some(c => c.origin !== "platform");

    const allSessionBuilt = allSessionComponentsBuilt(components);
    const isBuildComplete = !isResponding && !showBuildActivate && !deployFailure && allSessionBuilt;

    const handleRequestChanges = useCallback(() => {
        if (!feedbackText.trim()) return;
        onRequestChanges(feedbackText.trim());
        setFeedbackMode(false);
        setFeedbackText("");
    }, [feedbackText, onRequestChanges]);

    const handleFeedbackKeyDown = useCallback(
        (e: React.KeyboardEvent) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                e.preventDefault();
                handleRequestChanges();
            } else if (e.key === "Escape") {
                setFeedbackMode(false);
                setFeedbackText("");
            }
        },
        [handleRequestChanges]
    );

    const handleAddComment = useCallback(() => {
        if (!commentMenu) return;
        const { comp } = commentMenu;
        globalThis.dispatchEvent(
            new CustomEvent("follow-up-question", {
                detail: {
                    text: `${comp.name} (${componentTypeLabel(comp.type)})${comp.description ? ": " + comp.description : ""}`,
                    component: { name: comp.name, type: comp.type, icon: comp.icon, origin: comp.origin },
                },
            })
        );
        setCommentMenu(null);
    }, [commentMenu]);

    return (
        <div className="mb-2 rounded-lg border">
            <div className="flex items-center gap-2 px-4 pt-3 pb-2">
                {isApproved && (
                    <Button variant="ghost" size="sm" className="h-6 w-6 p-0" onClick={() => setIsCollapsed(!isCollapsed)}>
                        {isCollapsed ? <ChevronRight className="h-3.5 w-3.5" /> : <ChevronDown className="h-3.5 w-3.5" />}
                    </Button>
                )}
                <h3 className="flex-1 text-sm font-semibold">
                    <span className="font-normal text-(--secondary-text-wMain)">Build Plan: </span>
                    {displayName}
                </h3>
                {isBuildComplete && (
                    <div className="flex shrink-0 items-center gap-1.5">
                        <CircleCheckBig aria-label="Approved" className="h-4 w-4" style={{ color: "var(--success-wMain)" }} />
                        <span className="text-sm font-medium text-(--primary-text-wMain)">Approved</span>
                    </div>
                )}
            </div>

            {!isCollapsed && (
                <>
                    {manifest.description && <p className="px-4 pb-2 leading-relaxed">{manifest.description}</p>}

                    {manifest.risks && manifest.risks.length > 0 && (
                        <div className="px-4 py-3">
                            <MessageBanner
                                variant="warning"
                                message={
                                    <ul className="space-y-0.5">
                                        {manifest.risks.map(risk => (
                                            <li key={risk} className="leading-relaxed">
                                                {risk}
                                            </li>
                                        ))}
                                    </ul>
                                }
                            />
                        </div>
                    )}

                    {components.length > 0 && (
                        <div className="px-4 py-3">
                            <div className="mb-1.5 flex items-center gap-1.5 text-(--secondary-text-wMain)">Components ({components.length})</div>
                            <div className="space-y-1.5">
                                {components.map(comp => {
                                    const Icon = componentTypeIcon(comp.type);
                                    const connectorIcon = comp.type === "connector" && comp.icon && comp.icon !== "other" ? lookupConnectorIconByKey(comp.icon) : undefined;
                                    const isNew = comp.origin !== "platform";
                                    const blockedReason = isBlockedSessionComponent(comp) ? blockedReasonOf(comp) : null;
                                    const openCommentMenu = (anchor: HTMLElement) => {
                                        setCommentMenu({ comp, position: calculateMenuPosition(anchor.getBoundingClientRect()) });
                                    };
                                    return (
                                        <div key={comp.filename || comp.name}>
                                            <button
                                                type="button"
                                                aria-label={`Add a comment about ${humanizeComponentName(comp.name)}, ${componentTypeLabel(comp.type)}${isNew ? ", new" : ""}`}
                                                className={cn("card-surface card-surface-hover mt-2 flex w-full cursor-pointer items-center gap-2 rounded-md px-4 py-3 text-left transition-all")}
                                                onClick={e => openCommentMenu(e.currentTarget)}
                                            >
                                                {connectorIcon ? <img src={connectorIcon} alt={comp.icon!} className="h-4 w-4 shrink-0 object-contain" /> : <Icon className="h-4 w-4 shrink-0" style={{ color: typeColor(comp.type) }} />}
                                                <span className="min-w-0 flex-1 truncate" title={comp.name}>
                                                    {humanizeComponentName(comp.name)}
                                                </span>
                                                <span className="text-xs text-(--secondary-text-wMain)">{componentTypeLabel(comp.type)}</span>
                                                {blockedReason === null ? isNew && <StatusBadge status="new" /> : <StatusBadge status="blocked" />}
                                            </button>
                                            {blockedReason !== null && <p className="px-4 pt-1 text-xs text-(--warning-wMain)">Needs {blockedReason}</p>}
                                        </div>
                                    );
                                })}
                            </div>
                        </div>
                    )}

                    <EnvVarsSection envVars={envVars} open={showEnvVars} onToggle={() => setShowEnvVars(prev => !prev)} />

                    {manifest.architecture && (
                        <div className="px-4 py-3">
                            <div className="flex items-center gap-2">
                                <Button variant="ghost" size="sm" className="h-6 w-6 p-0" onClick={() => setShowArchitecture(prev => !prev)}>
                                    {showArchitecture ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
                                </Button>
                                <span className="flex-1 text-sm font-medium text-(--secondary-text-wMain)">Architecture</span>
                                {showArchitecture && !isApproved && (
                                    <Button
                                        size="sm"
                                        variant="ghost"
                                        onClick={() => {
                                            setArchEditText(manifest.architecture ?? "");
                                            setArchEditMode(true);
                                        }}
                                        title="Edit architecture"
                                    >
                                        <Pencil className="h-3.5 w-3.5" />
                                        Edit
                                    </Button>
                                )}
                            </div>
                            {showArchitecture && (
                                <div className="mt-1.5 pl-5 text-sm leading-relaxed">
                                    <MarkdownWrapper content={manifest.architecture.trim()} className="[&_h1]:!text-xl [&_h2]:!text-xl [&_h3]:!text-xl [&_p]:!whitespace-normal" />
                                </div>
                            )}
                        </div>
                    )}

                    <Dialog open={archEditMode} onOpenChange={open => !archSaving && setArchEditMode(open)}>
                        <DialogContent className="flex max-h-[80vh] flex-col sm:max-w-3xl" onOpenAutoFocus={e => e.preventDefault()}>
                            <DialogHeader>
                                <DialogTitle>Edit Architecture</DialogTitle>
                                <DialogDescription>Update the architecture section of the build plan. Markdown is supported.</DialogDescription>
                            </DialogHeader>
                            <div className="min-h-0 flex-1 pt-4">
                                <Textarea value={archEditText} onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) => setArchEditText(e.target.value)} rows={16} disabled={archSaving} className="resize-none text-sm leading-relaxed" autoFocus />
                            </div>
                            {archError && (
                                <p role="alert" className="text-sm text-(--error-wMain)">
                                    {archError}
                                </p>
                            )}
                            <DialogFooter>
                                <Button variant="outline" onClick={() => setArchEditMode(false)} disabled={archSaving}>
                                    Discard Changes
                                </Button>
                                <Button
                                    disabled={!archEditText.trim() || archSaving}
                                    onClick={async () => {
                                        const trimmed = archEditText.trim();
                                        if (!trimmed || trimmed === (manifest.architecture ?? "").trim()) {
                                            setArchEditMode(false);
                                            return;
                                        }
                                        if (onSaveManifest) {
                                            setArchSaving(true);
                                            setArchError(null);
                                            const result = await onSaveManifest(prev => ({ ...prev, architecture: trimmed }));
                                            setArchSaving(false);
                                            if (result.ok) {
                                                setArchEditMode(false);
                                                return;
                                            }
                                            setArchError(
                                                result.reason === "unreadable"
                                                    ? "The build plan can't be read right now, so saving would replace it with an older version. Ask the builder to repair it, then try again."
                                                    : "Couldn't save the build plan. Try again."
                                            );
                                            return;
                                        }
                                        onRequestChanges(`Replace the Architecture section of the build manifest with the following text, keeping everything else unchanged:\n\n${trimmed}`);
                                        setArchEditMode(false);
                                    }}
                                >
                                    {archSaving ? "Saving…" : "Save"}
                                </Button>
                            </DialogFooter>
                        </DialogContent>
                    </Dialog>

                    {feedbackMode && !isApproved && (
                        <div className="px-4 py-3">
                            <Textarea
                                ref={feedbackRef}
                                value={feedbackText}
                                onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) => setFeedbackText(e.target.value)}
                                onKeyDown={handleFeedbackKeyDown}
                                placeholder="Describe what you'd like to change..."
                                className="min-h-[60px] resize-y text-sm"
                                rows={2}
                                autoFocus
                            />
                            <div className="mt-2 flex justify-end gap-2">
                                <Button
                                    size="sm"
                                    variant="ghost"
                                    onClick={() => {
                                        setFeedbackMode(false);
                                        setFeedbackText("");
                                    }}
                                >
                                    Cancel
                                </Button>
                                <Button size="sm" onClick={handleRequestChanges} disabled={!feedbackText.trim()}>
                                    Send Feedback
                                </Button>
                            </div>
                        </div>
                    )}

                    {!isApproved && !feedbackMode && !isResponding && (
                        <div className="flex flex-col gap-2 px-4 py-2.5 sm:flex-row sm:items-center sm:justify-end">
                            <Button size="sm" variant="ghost" onClick={() => onRequestChanges("I'd like to iterate on this plan. Ask me, in a single question with a few examples, what I'd like to change.")} className="w-full gap-1.5 sm:w-auto">
                                Keep Editing
                            </Button>
                            {hasChangedComponents && (
                                <Button size="sm" onClick={() => requestActivate(onApprove)} className="w-full gap-1.5 sm:w-auto">
                                    Build &amp; Activate
                                </Button>
                            )}
                        </div>
                    )}
                </>
            )}

            {showBuildActivate && <PlatformCheckStatus view={platformCheck} onCheckAgain={onCheckAgain} onAskBuilder={onAskBuilder} />}

            <FailureAndAction failure={deployFailure} declined={declined} canRetry={canRetry} text={deployFailureText} actionButton={actionButton} />

            <InterruptedBuildNotice show={isApproved} onResume={onResumeBuild} />

            {(!isApproved || hasBuildToActivate) && <div className="px-4 pb-3 empty:hidden">{droppedNotice}</div>}

            <Dialog open={showConfirmDialog} onOpenChange={open => !open && dismissConfirmDialog()}>
                <DialogContent className="bg-(--background-w10) p-6 sm:max-w-md" onInteractOutside={dismissConfirmDialog}>
                    <DialogTitle className="leading-normal">Build &amp; Activate Plan</DialogTitle>
                    <DialogDescription className="mt-3 text-sm leading-relaxed">{buildActivateCopy(components)}</DialogDescription>
                    {manifest.risks && manifest.risks.length > 0 && (
                        <div className="mt-3 flex items-center gap-1.5 text-sm font-medium text-(--warning-wMain)">
                            <AlertTriangle className="h-4 w-4 shrink-0" />
                            {manifest.risks.length} {manifest.risks.length === 1 ? "risk" : "risks"} flagged — review them in the plan above before continuing.
                        </div>
                    )}
                    <label className="mt-4 flex items-center gap-2 text-sm">
                        <Checkbox checked={dontShowAgain} onCheckedChange={setDontShowAgain} />
                        Do not show me this again
                    </label>
                    <div className="mt-5 flex flex-col gap-2 sm:flex-row sm:justify-end">
                        <Button size="sm" variant="ghost" className="w-full sm:w-auto" onClick={dismissConfirmDialog}>
                            Cancel
                        </Button>
                        <Button
                            size="sm"
                            className="w-full sm:w-auto"
                            onClick={() => {
                                if (dontShowAgain) persistSkipActivateConfirm();
                                setShowConfirmDialog(false);
                                const action = pendingActivateRef.current;
                                pendingActivateRef.current = null;
                                action?.();
                            }}
                        >
                            Build &amp; Activate
                        </Button>
                    </div>
                </DialogContent>
            </Dialog>

            <AddCommentMenu isOpen={!!commentMenu} position={commentMenu?.position ?? null} onAddComment={handleAddComment} onClose={() => setCommentMenu(null)} />
        </div>
    );
}
