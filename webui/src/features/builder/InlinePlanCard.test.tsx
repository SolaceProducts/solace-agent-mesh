// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { StrictMode } from "react";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { TooltipProvider } from "@/ui/tooltip";

import { captureRadixDialogDiagnostics } from "@/testing/helpers/radixDialogDiagnostics";

import type { BuildManifest } from "./hooks/useBuilderManifest";
import { InlinePlanCard } from "./InlinePlanCard";
import type { PlatformCheck } from "./platformCheck";

const SKIP_CONFIRM_KEY = "sam_builder_skip_activate_confirm";

const confirmInDialog = () => fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: /^Build & Activate$/i }));

const manifest: BuildManifest = {
    name: "build",
    display_name: "Text Summarizer",
    components: [{ filename: "summarizer.yaml", type: "agent", name: "SummarizesText", status: "deployed", origin: "session" }],
};

function renderCard(props: Partial<React.ComponentProps<typeof InlinePlanCard>> = {}) {
    return render(
        <TooltipProvider>
            <InlinePlanCard manifest={manifest} onApprove={vi.fn()} onRequestChanges={vi.fn()} isApproved={false} {...props} />
        </TooltipProvider>
    );
}

describe("InlinePlanCard — in-chat Build & Activate (iterate)", () => {
    beforeEach(() => localStorage.clear());

    test("the in-chat Build & Activate shows the confirm dialog first, then deploys on confirm (never re-approves)", () => {
        const onBuildActivate = vi.fn();
        const onApprove = vi.fn();
        renderCard({ isApproved: true, canBuildActivate: true, onBuildActivate, onApprove });

        fireEvent.click(screen.getByRole("button", { name: /^Build & Activate$/i }));
        expect(screen.getByText("Build & Activate Plan")).toBeInTheDocument();
        expect(onBuildActivate).not.toHaveBeenCalled();
        confirmInDialog();
        expect(onBuildActivate).toHaveBeenCalledTimes(1);
        expect(onApprove).not.toHaveBeenCalled();
    });

    test("cancelling the confirm dialog does NOT deploy and closes the dialog", () => {
        const onBuildActivate = vi.fn();
        renderCard({ isApproved: true, canBuildActivate: true, onBuildActivate, onApprove: vi.fn() });

        fireEvent.click(screen.getByRole("button", { name: /^Build & Activate$/i }));
        fireEvent.click(screen.getByRole("button", { name: /^Cancel$/i }));
        expect(onBuildActivate).not.toHaveBeenCalled();
        expect(screen.queryByText("Build & Activate Plan")).toBeNull();
    });

    test("re-opening after a cancel fires the freshly-armed action, not the cancelled one", () => {
        const onBuildActivate = vi.fn();
        const onApprove = vi.fn();
        renderCard({ isApproved: true, canBuildActivate: true, onBuildActivate, onApprove });

        fireEvent.click(screen.getByRole("button", { name: /^Build & Activate$/i }));
        fireEvent.click(screen.getByRole("button", { name: /^Cancel$/i }));
        fireEvent.click(screen.getByRole("button", { name: /^Build & Activate$/i }));
        confirmInDialog();
        expect(onBuildActivate).toHaveBeenCalledTimes(1);
        expect(onApprove).not.toHaveBeenCalled();
    });

    test("falls safe to showing the dialog when localStorage throws (private-mode embeds)", () => {
        const onBuildActivate = vi.fn();
        const getItem = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
            throw new DOMException("denied", "SecurityError");
        });
        try {
            renderCard({ isApproved: true, canBuildActivate: true, onBuildActivate, onApprove: vi.fn() });
            fireEvent.click(screen.getByRole("button", { name: /^Build & Activate$/i }));
            expect(screen.getByText("Build & Activate Plan")).toBeInTheDocument();
            expect(onBuildActivate).not.toHaveBeenCalled();
        } finally {
            getItem.mockRestore();
        }
    });

    test("ticking 'do not show again' then confirming persists the skip key", () => {
        const onBuildActivate = vi.fn();
        renderCard({ isApproved: true, canBuildActivate: true, onBuildActivate, onApprove: vi.fn() });

        fireEvent.click(screen.getByRole("button", { name: /^Build & Activate$/i }));
        fireEvent.click(screen.getByRole("checkbox"));
        confirmInDialog();
        expect(localStorage.getItem(SKIP_CONFIRM_KEY)).toBe("true");
        expect(onBuildActivate).toHaveBeenCalledTimes(1);
    });

    test("the in-chat Build & Activate deploys without a dialog once 'do not show again' is set", () => {
        const onBuildActivate = vi.fn();
        localStorage.setItem(SKIP_CONFIRM_KEY, "true");
        renderCard({ isApproved: true, canBuildActivate: true, onBuildActivate, onApprove: vi.fn() });
        fireEvent.click(screen.getByRole("button", { name: /^Build & Activate$/i }));
        expect(screen.queryByText("Build & Activate Plan")).toBeNull();
        expect(onBuildActivate).toHaveBeenCalledTimes(1);
    });

    test("hides the deploy button while the agent is still responding", () => {
        renderCard({ isApproved: true, canBuildActivate: true, isResponding: true, onBuildActivate: vi.fn() });
        expect(screen.queryByRole("button", { name: /Build & Activate/i })).toBeNull();
    });

    test("shows 'Activating…' and disables the button while a deploy is in flight", () => {
        renderCard({ isApproved: true, canBuildActivate: true, isBuildActivating: true, onBuildActivate: vi.fn() });
        const btn = screen.getByRole("button", { name: /Activating/i });
        expect(btn).toBeDisabled();
    });

    test("does not show the deploy button when there are no undeployed changes", () => {
        renderCard({ isApproved: true, canBuildActivate: false, onBuildActivate: vi.fn() });
        expect(screen.queryByRole("button", { name: /Build & Activate/i })).toBeNull();
        expect(screen.queryByRole("button", { name: /Activating/i })).toBeNull();
    });

    test("does not show the deploy button when no onBuildActivate handler is provided", () => {
        renderCard({ isApproved: true, canBuildActivate: true, onBuildActivate: undefined });
        expect(screen.queryByRole("button", { name: /Build & Activate/i })).toBeNull();
    });

    test("pre-approval still shows the approve button (onApprove path), not the deploy bridge", () => {
        const onApprove = vi.fn();
        const onBuildActivate = vi.fn();
        localStorage.setItem(SKIP_CONFIRM_KEY, "true");
        renderCard({ isApproved: false, canBuildActivate: false, onApprove, onBuildActivate });

        fireEvent.click(screen.getByRole("button", { name: /Build & Activate/i }));
        expect(onApprove).toHaveBeenCalledTimes(1);
        expect(onBuildActivate).not.toHaveBeenCalled();
    });

    test("the plan-card approve button also routes through the confirm dialog, then approves on confirm", () => {
        const onApprove = vi.fn();
        renderCard({ isApproved: false, canBuildActivate: false, onApprove });

        fireEvent.click(screen.getByRole("button", { name: /^Build & Activate$/i }));
        expect(screen.getByText("Build & Activate Plan")).toBeInTheDocument();
        expect(onApprove).not.toHaveBeenCalled();
        confirmInDialog();
        expect(onApprove).toHaveBeenCalledTimes(1);
    });
});

describe("InlinePlanCard — the platform check above Build & Activate", () => {
    beforeEach(() => localStorage.clear());

    const problem = { kind: "agent", name: "SummarizesText", message: "model alias is unknown" };
    const buildActivate = () => screen.queryByRole("button", { name: /^Build & Activate$/i });

    const BLOCKING: Array<[string, PlatformCheck, string]> = [
        ["checking", { kind: "checking" }, "Checking with the platform…"],
        ["unavailable", { kind: "unavailable", message: "down" }, "Couldn't check with the platform"],
        ["refused", { kind: "refused", problems: [problem], forbidden: [] }, "agent SummarizesText: model alias is unknown"],
        ["notAllowed", { kind: "notAllowed", problems: [problem] }, "agent SummarizesText: model alias is unknown"],
    ];

    test.each(BLOCKING)("with the check %s the status shows and Build & Activate is there but disabled", (_label, check, text) => {
        renderCard({ isApproved: true, canBuildActivate: false, onBuildActivate: vi.fn(), platformCheck: { check, canAskBuilder: false } });

        expect(screen.getByText(text)).toBeInTheDocument();
        expect(buildActivate()).toBeDisabled();
    });

    test("a passed check reads Ready, lists its warnings and leaves Build & Activate enabled", () => {
        renderCard({ isApproved: true, canBuildActivate: true, onBuildActivate: vi.fn(), platformCheck: { check: { kind: "passed", warnings: ["Skill Y is unchanged"] }, canAskBuilder: false } });

        expect(screen.getByText("Ready")).toBeInTheDocument();
        expect(screen.getByText("Skill Y is unchanged")).toBeInTheDocument();
        expect(buildActivate()).toBeEnabled();
    });

    test("a disabled Build & Activate does not open the confirmation", () => {
        renderCard({ isApproved: true, canBuildActivate: false, onBuildActivate: vi.fn(), platformCheck: { check: { kind: "checking" }, canAskBuilder: false } });

        fireEvent.click(buildActivate()!);

        expect(screen.queryByText("Build & Activate Plan")).toBeNull();
    });

    test("Check again and Ask the Builder reach the host", () => {
        const onCheckAgain = vi.fn();
        const onAskBuilder = vi.fn();
        renderCard({ isApproved: true, canBuildActivate: false, onBuildActivate: vi.fn(), onCheckAgain, onAskBuilder, platformCheck: { check: { kind: "refused", problems: [problem], forbidden: [] }, canAskBuilder: true } });

        fireEvent.click(screen.getByRole("button", { name: "Check again" }));
        fireEvent.click(screen.getByRole("button", { name: "Ask the Builder to fix these" }));

        expect(onCheckAgain).toHaveBeenCalledTimes(1);
        expect(onAskBuilder).toHaveBeenCalledTimes(1);
    });

    test("an idle check renders nothing new", () => {
        renderCard({ isApproved: true, canBuildActivate: true, onBuildActivate: vi.fn(), platformCheck: { check: { kind: "idle" }, canAskBuilder: false } });

        expect(screen.queryByText("Ready")).toBeNull();
        expect(buildActivate()).toBeEnabled();
    });

    test("no status while the agent is still responding", () => {
        renderCard({ isApproved: true, isResponding: true, canBuildActivate: false, onBuildActivate: vi.fn(), platformCheck: { check: { kind: "checking" }, canAskBuilder: false } });

        expect(screen.queryByText("Checking with the platform…")).toBeNull();
    });
});

describe("InlinePlanCard — an approved build that was interrupted", () => {
    beforeEach(() => localStorage.clear());

    test("offers Resume build with a note, and resuming runs the handler", () => {
        const onResumeBuild = vi.fn();
        renderCard({ isApproved: true, onResumeBuild });

        expect(screen.getByText(/This build was interrupted before it finished/i)).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: /^Resume build$/i }));
        expect(onResumeBuild).toHaveBeenCalledTimes(1);
    });

    test("is still offered once the approved card has collapsed", () => {
        renderCard({ isApproved: true, isCollapsed: true, onResumeBuild: vi.fn() });

        expect(screen.getByRole("button", { name: /^Resume build$/i })).toBeInTheDocument();
    });

    test("is not offered before the plan is approved", () => {
        renderCard({ isApproved: false, onResumeBuild: vi.fn() });

        expect(screen.queryByRole("button", { name: /Resume build/i })).toBeNull();
        expect(screen.getByRole("button", { name: /Keep Editing/i })).toBeInTheDocument();
    });

    test("is not offered when no resume handler is provided (the build is not interrupted)", () => {
        renderCard({ isApproved: true, onResumeBuild: undefined });

        expect(screen.queryByRole("button", { name: /Resume build/i })).toBeNull();
        expect(screen.queryByText(/This build was interrupted/i)).toBeNull();
    });

    test("a plan that can be built offers Build & Activate and Resume build side by side", () => {
        renderCard({ isApproved: true, onResumeBuild: vi.fn(), canBuildActivate: true, onBuildActivate: vi.fn() });

        expect(screen.getByRole("button", { name: /^Build & Activate$/i })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: /^Resume build$/i })).toBeInTheDocument();
    });
});

describe("InlinePlanCard — completion check is gated on the build being created", () => {
    const draftManifest: BuildManifest = {
        name: "build",
        display_name: "Text Summarizer",
        components: [{ filename: "summarizer.yaml", type: "agent", name: "SummarizesText", status: "draft", origin: "session" }],
    };
    const render1 = (m: BuildManifest, props: Partial<React.ComponentProps<typeof InlinePlanCard>> = {}) =>
        render(
            <TooltipProvider>
                <InlinePlanCard manifest={m} onApprove={vi.fn()} onRequestChanges={vi.fn()} isApproved={false} {...props} />
            </TooltipProvider>
        );

    test("shows the check once every session component is built and the turn settled", () => {
        render1(manifest, { isApproved: true, isResponding: false });
        expect(screen.getByLabelText("Approved")).toBeInTheDocument();
    });

    test("hides the check while a component is still a draft, even after approval", () => {
        render1(draftManifest, { isApproved: true, isResponding: false });
        expect(screen.queryByLabelText("Approved")).toBeNull();
    });

    test("hides the check while the agent is still responding (build in progress)", () => {
        render1(manifest, { isApproved: true, isResponding: true });
        expect(screen.queryByLabelText("Approved")).toBeNull();
    });

    test("hides the check for a platform-only plan (no new session components), even when approved and settled", () => {
        const platformOnly: BuildManifest = {
            name: "build",
            display_name: "Reuse existing",
            components: [{ filename: "a.yaml", type: "agent", name: "Existing", status: "deployed", origin: "platform" }],
        };
        render1(platformOnly, { isApproved: true, isResponding: false });
        expect(screen.queryByLabelText("Approved")).toBeNull();
    });

    test("hides the check when session components have mixed statuses (one built, one still draft)", () => {
        const mixed: BuildManifest = {
            name: "build",
            display_name: "Two agents",
            components: [
                { filename: "a.yaml", type: "agent", name: "A", status: "deployed", origin: "session" },
                { filename: "b.yaml", type: "agent", name: "B", status: "draft", origin: "session" },
            ],
        };
        render1(mixed, { isApproved: true, isResponding: false });
        expect(screen.queryByLabelText("Approved")).toBeNull();
    });

    test("shows the check for a skill-only build whose skill is validated (skills terminate at validated, not deployed)", () => {
        const skill: BuildManifest = {
            name: "build",
            display_name: "Skill",
            components: [{ filename: "s.yaml", type: "skill", name: "sample-geography", status: "validated", origin: "session" }],
        };
        render1(skill, { isApproved: true, isResponding: false });
        expect(screen.getByLabelText("Approved")).toBeInTheDocument();
    });
});

describe("InlinePlanCard — one call to action once the build is generated (Approved vs Build & Activate)", () => {
    const sessionManifest = (status: "draft" | "validated" | "deployed"): BuildManifest => ({
        name: "build",
        display_name: "Text Summarizer",
        components: [{ filename: "summarizer.yaml", type: "agent", name: "SummarizesText", status, origin: "session" }],
    });
    const buildActivate = () => screen.queryByRole("button", { name: /^Build & Activate$/i });
    const approvedCheck = () => screen.queryByLabelText("Approved");

    test.each([
        { state: "generated while the user was away, nothing saved", status: "validated", props: { canBuildActivate: true }, check: false, button: true },
        { state: "generated, auto-save/deploy still driving it", status: "validated", props: { canBuildActivate: false }, check: true, button: false },
        { state: "created and deployed", status: "deployed", props: { canBuildActivate: false }, check: true, button: false },
        { state: "iterating on a deployed build with undeployed edits", status: "validated", props: { canBuildActivate: true }, check: false, button: true },
        { state: "still generating", status: "draft", props: { canBuildActivate: false }, check: false, button: false },
        { state: "generated while the agent is still responding", status: "validated", props: { canBuildActivate: true, isResponding: true }, check: false, button: false },
    ] as const)("$state", ({ status, props, check, button }) => {
        renderCard({ manifest: sessionManifest(status), isApproved: true, onBuildActivate: vi.fn(), ...props });

        expect(!!approvedCheck()).toBe(check);
        expect(!!buildActivate()).toBe(button);
    });

    test("a failure with Try again on offer shows no check either", () => {
        renderCard({ manifest: sessionManifest("validated"), isApproved: true, canBuildActivate: false, onRetry: vi.fn(), deployFailure: { message: "boom", canRetry: true } });

        expect(screen.getByRole("button", { name: /try again/i })).toBeInTheDocument();
        expect(approvedCheck()).toBeNull();
    });

    const cardInputs = (["draft", "validated", "deployed"] as const).flatMap(status =>
        [true, false].flatMap(canBuildActivate =>
            [true, false].flatMap(isResponding =>
                [null, { message: "boom", canRetry: true }].map(deployFailure => ({
                    label: `${status} canBuildActivate=${canBuildActivate} isResponding=${isResponding} failure=${!!deployFailure}`,
                    status,
                    canBuildActivate,
                    isResponding,
                    deployFailure,
                }))
            )
        )
    );

    test.each(cardInputs)("never shows the check beside Build & Activate or Try again: $label", ({ label, status, canBuildActivate, isResponding, deployFailure }) => {
        renderCard({ manifest: sessionManifest(status), isApproved: true, canBuildActivate, isResponding, deployFailure, onBuildActivate: vi.fn(), onRetry: vi.fn() });

        expect(!!approvedCheck() && !!buildActivate(), label).toBe(false);
        expect(!!approvedCheck() && !!screen.queryByRole("button", { name: /try again/i }), label).toBe(false);
    });
});

describe("InlinePlanCard — component row 'Add Comment' popover (Quick Build consistency)", () => {
    let followUp: ReturnType<typeof vi.fn>;

    beforeEach(() => {
        localStorage.clear();
        followUp = vi.fn();
        window.addEventListener("follow-up-question", followUp as EventListener);
    });

    afterEach(() => {
        window.removeEventListener("follow-up-question", followUp as EventListener);
    });

    const getRow = () => screen.getByRole("button", { name: /SummarizesText/i });

    test("clicking a component row opens the Add Comment popover and does NOT dispatch immediately", () => {
        renderCard();
        expect(screen.queryByRole("button", { name: /^add comment$/i })).toBeNull();
        expect(screen.queryByText(/add comment/i)).toBeNull();

        fireEvent.click(getRow());

        expect(screen.getByRole("button", { name: /^add comment$/i })).toBeInTheDocument();
        expect(followUp).not.toHaveBeenCalled();
    });

    test("clicking 'Add Comment' dispatches follow-up-question with the component payload, then closes the popover", () => {
        renderCard();
        fireEvent.click(getRow());
        fireEvent.click(screen.getByRole("button", { name: /^add comment$/i }));

        expect(followUp).toHaveBeenCalledTimes(1);
        const detail = (followUp.mock.calls[0][0] as CustomEvent).detail;
        expect(detail.component).toMatchObject({ name: "SummarizesText", type: "agent" });
        expect(detail.text).toContain("SummarizesText");
        expect(screen.queryByRole("button", { name: /^add comment$/i })).toBeNull();
    });

    test("the component row is a native button, so keyboard activation is handled by the platform", () => {
        renderCard();
        expect(getRow().tagName).toBe("BUTTON");
    });

    test("opening the popover moves focus to the Add Comment action so keyboard users can reach it", () => {
        renderCard();
        fireEvent.click(getRow());
        expect(screen.getByRole("button", { name: /^add comment$/i })).toHaveFocus();
    });

    test("dispatches the follow-up-question exactly once under StrictMode (no impure setter double-fire)", () => {
        render(
            <StrictMode>
                <TooltipProvider>
                    <InlinePlanCard manifest={manifest} onApprove={vi.fn()} onRequestChanges={vi.fn()} isApproved={false} />
                </TooltipProvider>
            </StrictMode>
        );
        fireEvent.click(getRow());
        fireEvent.click(screen.getByRole("button", { name: /^add comment$/i }));
        expect(followUp).toHaveBeenCalledTimes(1);
    });

    test("clicking a different row before confirming re-anchors the popover to the newly clicked component", () => {
        const twoComps: BuildManifest = {
            name: "build",
            display_name: "Two components",
            components: [
                { filename: "a.yaml", type: "agent", name: "SummarizesText", status: "deployed", origin: "session" },
                { filename: "b.yaml", type: "connector", name: "PostgresLookup", status: "validated", origin: "session" },
            ],
        };
        renderCard({ manifest: twoComps });
        fireEvent.click(screen.getByRole("button", { name: /SummarizesText/i }));
        fireEvent.click(screen.getByRole("button", { name: /PostgresLookup/i }));
        fireEvent.click(screen.getByRole("button", { name: /^add comment$/i }));

        expect(followUp).toHaveBeenCalledTimes(1);
        const detail = (followUp.mock.calls[0][0] as CustomEvent).detail;
        expect(detail.component).toMatchObject({ name: "PostgresLookup", type: "connector" });
    });
});

describe("InlinePlanCard — activate dialog copy by component type", () => {
    beforeEach(() => localStorage.clear());

    const planWith = (type: string, name: string): BuildManifest => ({
        name: "build",
        display_name: name,
        components: [{ filename: `${name}.yaml`, type, name, status: "draft", origin: "session" } as NonNullable<BuildManifest["components"]>[number]],
    });

    const openConfirmDialog = (m: BuildManifest) => {
        render(
            <TooltipProvider>
                <InlinePlanCard manifest={m} onApprove={vi.fn()} onRequestChanges={vi.fn()} isApproved={false} />
            </TooltipProvider>
        );
        fireEvent.click(screen.getByRole("button", { name: /^Build & Activate$/i }));
        return within(screen.getByRole("dialog"));
    };

    test("an a2a_agent shows deploy-live copy, never 'attach it to an agent'", () => {
        const dialog = openConfirmDialog(planWith("a2a_agent", "MultiplicationAgent"));
        expect(dialog.queryByText(/attach it to an agent/i)).toBeNull();
        expect(dialog.getByText(/deploy them live/i)).toBeInTheDocument();
    });

    test("a skill still shows the attach-to-an-agent copy", () => {
        const dialog = openConfirmDialog(planWith("skill", "Summarize"));
        expect(dialog.getByText(/attach it to an agent/i)).toBeInTheDocument();
    });

    test("a mixed skill + a2a_agent plan names only the skill as attach-only", () => {
        const mixed: BuildManifest = {
            name: "build",
            display_name: "Mixed",
            components: [
                { filename: "s.yaml", type: "skill", name: "Summarize", status: "draft", origin: "session" },
                { filename: "a.yaml", type: "a2a_agent", name: "MultiplicationAgent", status: "draft", origin: "session" },
            ] as NonNullable<BuildManifest["components"]>,
        };
        const copy = openConfirmDialog(mixed).getByText(/must be attached to an agent to take effect/i);
        expect(copy).toHaveTextContent(/your skill must be attached/i);
        expect(copy).not.toHaveTextContent(/a2a_agent/i);
    });

    test("an existing (platform) toolset alongside a new workflow does not add attach-only copy", () => {
        const plan: BuildManifest = {
            name: "build",
            display_name: "Weather",
            components: [
                { filename: "wf.yaml", type: "workflow", name: "WeatherWorkflow", status: "draft", origin: "session" },
                { filename: "", type: "toolset", name: "WeatherToolset", status: "deployed", origin: "platform" },
            ] as NonNullable<BuildManifest["components"]>,
        };
        const dialog = openConfirmDialog(plan);
        expect(dialog.queryByText(/attach/i)).toBeNull();
        expect(dialog.getByText(/deploy them live/i)).toBeInTheDocument();
    });
});

describe("InlinePlanCard — entrypoint component in the plan list", () => {
    const plan: BuildManifest = {
        name: "build",
        display_name: "Orders",
        components: [{ filename: "order_events_entrypoint.yaml", type: "gateway", name: "order_events_entrypoint", status: "validated", origin: "session" }] as NonNullable<BuildManifest["components"]>,
    };

    test("labels the row 'Entrypoint' in text and aria-label, never 'Gateway'", () => {
        render(
            <TooltipProvider>
                <InlinePlanCard manifest={plan} onApprove={vi.fn()} onRequestChanges={vi.fn()} isApproved={false} />
            </TooltipProvider>
        );
        const row = screen.getByRole("button", { name: /Order Events Entrypoint, Entrypoint, new/ });
        expect(within(row).getByText("Entrypoint")).toBeInTheDocument();
        expect(screen.queryByText(/gateway/i)).toBeNull();
        expect(row.getAttribute("aria-label")).not.toMatch(/gateway/i);
    });

    test("the comment quote says 'Entrypoint' while the structured component type stays 'gateway'", () => {
        const followUp = vi.fn();
        window.addEventListener("follow-up-question", followUp as EventListener);
        try {
            render(
                <TooltipProvider>
                    <InlinePlanCard manifest={plan} onApprove={vi.fn()} onRequestChanges={vi.fn()} isApproved={false} />
                </TooltipProvider>
            );
            fireEvent.click(screen.getByRole("button", { name: /Order Events Entrypoint/ }));
            fireEvent.click(screen.getByRole("button", { name: /^add comment$/i }));
            const detail = (followUp.mock.calls[0][0] as CustomEvent).detail;
            expect(detail.text).toContain("(Entrypoint)");
            expect(detail.text).not.toMatch(/gateway/i);
            expect(detail.component).toMatchObject({ type: "gateway" });
        } finally {
            window.removeEventListener("follow-up-question", followUp as EventListener);
        }
    });
});

describe("InlinePlanCard — toolset component in the plan list", () => {
    const plan: BuildManifest = {
        name: "build",
        display_name: "Weather",
        components: [
            { filename: "wf.yaml", type: "workflow", name: "WeatherWorkflow", status: "validated", origin: "session" },
            { filename: "", type: "toolset", name: "WeatherToolset", status: "deployed", origin: "platform" },
        ] as NonNullable<BuildManifest["components"]>,
    };

    test("lists the toolset with a 'Toolset' label and counts it in Components (N)", () => {
        render(
            <TooltipProvider>
                <InlinePlanCard manifest={plan} onApprove={vi.fn()} onRequestChanges={vi.fn()} isApproved={false} />
            </TooltipProvider>
        );
        expect(screen.getByText(/Components \(2\)/)).toBeInTheDocument();
        const row = screen.getByRole("button", { name: /WeatherToolset, toolset/i });
        expect(within(row).getByText("Toolset")).toBeInTheDocument();
        expect(within(row).queryByText("new")).toBeNull();
    });
});

describe("InlinePlanCard — deploy failure (in-plan retry)", () => {
    beforeEach(() => localStorage.clear());

    test("shows a simplified error banner and a 'Try again' action (no 'Build & Activate', no confirm dialog)", () => {
        const onRetry = vi.fn();
        renderCard({
            isApproved: true,
            canBuildActivate: true,
            onBuildActivate: vi.fn(),
            onRetry,
            deployFailure: { message: "Deploy failed: order_exception_gateway (internal error)", canRetry: true },
        });

        expect(screen.getByText(/couldn.t be completed:/i)).toBeInTheDocument();
        expect(screen.getByText(/internal error/)).toBeInTheDocument();

        expect(screen.queryByRole("button", { name: /^Build & Activate$/i })).toBeNull();
        const retry = screen.getByRole("button", { name: /^Try again$/i });
        fireEvent.click(retry);
        expect(screen.queryByText("Build & Activate Plan")).toBeNull();
        expect(onRetry).toHaveBeenCalledTimes(1);
    });

    test("surfaces the raw error inline for a terminal failure and keeps it in the hover title", () => {
        renderCard({
            isApproved: true,
            canBuildActivate: true,
            onRetry: vi.fn(),
            deployFailure: { message: "Deploy failed: order_exception_gateway (internal error)", canRetry: true },
        });
        expect(screen.getByText(/couldn.t be completed: Deploy failed: order_exception_gateway \(internal error\)/)).toHaveAttribute("title", "Deploy failed: order_exception_gateway (internal error)");
    });

    test("disables 'Try again' while a save/deploy is in flight", () => {
        renderCard({
            isApproved: true,
            canBuildActivate: true,
            onRetry: vi.fn(),
            isBuildActivating: true,
            deployFailure: { message: "Deploy failed: X", canRetry: true },
        });
        expect(screen.getByRole("button", { name: /^Try again$/i })).toBeDisabled();
    });

    test("while the builder is auto-fixing (canRetry:false), shows a 'fixing' note and no retry button", () => {
        renderCard({
            isApproved: true,
            canBuildActivate: true,
            onRetry: vi.fn(),
            deployFailure: { message: "validation error", canRetry: false },
        });
        expect(screen.getByText(/builder is fixing this/i)).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /^Try again$/i })).toBeNull();
    });
});

describe("InlinePlanCard — primary CTA gating on a freshly-loaded workflow", () => {
    const loadedUnchanged: BuildManifest = {
        name: "workflow_x",
        display_name: "DocumentProcessor",
        components: [
            { filename: "wf.yaml", type: "workflow", name: "DocumentProcessor", status: "deployed", origin: "platform" },
            { filename: "agent.yaml", type: "agent", name: "DocumentAnalyzer", status: "deployed", origin: "platform" },
        ],
    };

    test("withholds 'Build & Activate' (offers only 'Keep Editing') when every component is unchanged (origin platform)", () => {
        render(
            <TooltipProvider>
                <InlinePlanCard manifest={loadedUnchanged} onApprove={vi.fn()} onRequestChanges={vi.fn()} isApproved={false} />
            </TooltipProvider>
        );
        expect(screen.getByRole("button", { name: /^Keep Editing$/i })).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /^Build & Activate$/i })).toBeNull();
    });

    test("shows 'Build & Activate' once a new/changed (session-origin) component is present", () => {
        const edited: BuildManifest = { ...loadedUnchanged, components: [...loadedUnchanged.components!, { filename: "new.yaml", type: "agent", name: "SentimentAgent", status: "draft", origin: "session" }] };
        render(
            <TooltipProvider>
                <InlinePlanCard manifest={edited} onApprove={vi.fn()} onRequestChanges={vi.fn()} isApproved={false} />
            </TooltipProvider>
        );
        expect(screen.getByRole("button", { name: /^Build & Activate$/i })).toBeInTheDocument();
    });
});

describe("InlinePlanCard — deploy-failure banner surfaces the real reason", () => {
    test("terminal failure shows the actual backend message (not a generic dead-end)", () => {
        renderCard({ isApproved: true, deployFailure: { message: "Bot Token must start with 'xoxb-'", canRetry: true } });
        expect(screen.getByText(/This build couldn’t be completed: Bot Token must start with 'xoxb-'/)).toBeInTheDocument();
    });

    test("transient (auto-recovering) failure keeps the generic 'retrying' note, not the raw message", () => {
        renderCard({ isApproved: true, deployFailure: { message: "Bot Token must start with 'xoxb-'", canRetry: false } });
        expect(screen.getByText(/the builder is retrying/)).toBeInTheDocument();
        expect(screen.queryByText(/This build couldn’t be completed:/)).not.toBeInTheDocument();
    });

    test("terminal failure with an empty message falls back to the generic copy", () => {
        renderCard({ isApproved: true, deployFailure: { message: "", canRetry: true } });
        expect(screen.getByText(/Try again to finish setting it up/)).toBeInTheDocument();
    });
});

describe("InlinePlanCard — a declined question is not a failure", () => {
    const declined = { message: "Save cancelled. Try again to review and save.", canRetry: true, declined: true } as const;

    test("says what happened without calling the build failed, as information rather than an error, with no hover error, and offers Try again", () => {
        const onRetry = vi.fn();
        renderCard({ isApproved: true, canBuildActivate: false, onRetry, deployFailure: declined });

        const note = screen.getByText("Save cancelled. Try again to review and save.");
        expect(note).not.toHaveAttribute("title");
        expect(screen.queryByText(/couldn.t be completed/)).toBeNull();
        expect(screen.getByTestId("messageBanner").className).not.toContain("error");
        fireEvent.click(screen.getByRole("button", { name: /^Try again$/i }));
        expect(onRetry).toHaveBeenCalledTimes(1);
    });

    test("a failure with the same text and no declined mark is still worded as a failure", () => {
        renderCard({ isApproved: true, canBuildActivate: false, onRetry: vi.fn(), deployFailure: { message: declined.message, canRetry: true } });

        expect(screen.getByText(/This build couldn’t be completed: Save cancelled/)).toBeInTheDocument();
    });

    const ROWS = [true, false].flatMap(isApproved =>
        [true, false].flatMap(canBuildActivate =>
            [true, false].flatMap(isResponding => [true, false].map(hasHandler => ({ isApproved, canBuildActivate, isResponding, hasHandler, onScreen: isApproved && canBuildActivate && !isResponding && hasHandler })))
        )
    );

    test.each(ROWS)(
        "a decline with approved=$isApproved canBuildActivate=$canBuildActivate responding=$isResponding handler=$hasHandler shows the banner with Try again unless Build & Activate is on the card",
        ({ isApproved, canBuildActivate, isResponding, hasHandler, onScreen }) => {
            renderCard({ isApproved, canBuildActivate, isResponding, onBuildActivate: hasHandler ? vi.fn() : undefined, onRetry: vi.fn(), deployFailure: declined });

            expect(screen.queryByText(declined.message) !== null).toBe(!onScreen);
            expect(screen.queryByRole("button", { name: /^Try again$/i }) !== null).toBe(!onScreen);
            if (isApproved) expect(screen.queryByRole("button", { name: /^Build & Activate$/i }) !== null).toBe(onScreen);
        }
    );

    test("a failure that is not a decline is never hidden by the button being on the card's terms", () => {
        renderCard({ isApproved: true, canBuildActivate: true, onBuildActivate: vi.fn(), onRetry: vi.fn(), deployFailure: { message: "boom", canRetry: true } });

        expect(screen.getByText(/couldn.t be completed: boom/)).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /^Build & Activate$/i })).toBeNull();
    });
});

describe("InlinePlanCard — Edit Architecture save", () => {
    beforeEach(() => localStorage.clear());

    const openArchEditor = () => {
        const header = screen.getByText("Architecture").closest("div") as HTMLElement;
        fireEvent.click(within(header).getAllByRole("button")[0]);
        fireEvent.click(screen.getByRole("button", { name: /^Edit$/i }));
        fireEvent.change(screen.getByRole("textbox"), { target: { value: "New architecture text" } });
    };

    test("explains a refusal instead of failing silently, and keeps what was typed", async () => {
        const onSaveManifest = vi.fn().mockResolvedValue({ ok: false, reason: "unreadable" });
        renderCard({ manifest: { ...manifest, architecture: "Old text" }, onSaveManifest });

        openArchEditor();
        fireEvent.click(screen.getByRole("button", { name: /^Save$/i }));

        const alert = await screen.findByRole("alert");
        expect(alert.textContent).toMatch(/can't be read/i);
        expect(screen.getByRole("textbox")).toHaveValue("New architecture text");
    });

    test("says nothing on success", async () => {
        const onSaveManifest = vi.fn().mockResolvedValue({ ok: true });
        renderCard({ manifest: { ...manifest, architecture: "Old text" }, onSaveManifest });

        openArchEditor();
        fireEvent.click(screen.getByRole("button", { name: /^Save$/i }));

        await vi.waitFor(() => expect(onSaveManifest).toHaveBeenCalledTimes(1));
        expect(screen.queryByRole("alert")).toBeNull();
    });
});

describe("InlinePlanCard — Build & Activate confirm dialog accessibility", () => {
    beforeEach(() => localStorage.clear());

    test("the confirm dialog is named and described, and opening it emits no Radix diagnostics", () => {
        const diagnostics = captureRadixDialogDiagnostics();
        try {
            renderCard({ isApproved: true, canBuildActivate: true, onBuildActivate: vi.fn(), onApprove: vi.fn() });

            fireEvent.click(screen.getByRole("button", { name: /^Build & Activate$/i }));

            expect(screen.getByRole("dialog", { name: "Build & Activate Plan", description: /^This will generate all component configurations and deploy them live\./ })).toBeInTheDocument();
            expect(diagnostics.messages()).toEqual([]);
        } finally {
            diagnostics.restore();
        }
    });
});

describe("InlinePlanCard — a planned component the Builder could not create", () => {
    const blockedManifest: BuildManifest = {
        name: "build",
        display_name: "Order Fulfillment",
        components: [
            { filename: "inventory_api.json", type: "connector", name: "inventory_api", status: "blocked", origin: "session", blocked_reason: "the http(s) URL of the Inventory API's OpenAPI document." },
            { filename: "checker.yaml", type: "agent", name: "InventoryChecker", status: "validated", origin: "session" },
        ],
    };

    test("the row says it needs input and what is needed, instead of reading as a built component", () => {
        renderCard({ manifest: blockedManifest, isApproved: true });
        expect(screen.getByText("needs input")).toBeInTheDocument();
        expect(screen.getByText("Needs the http(s) URL of the Inventory API's OpenAPI document")).toBeInTheDocument();
    });

    test("the plan reads Approved only when every component is built, which a blocked one is not", () => {
        renderCard({ manifest: blockedManifest, isApproved: true });
        expect(screen.queryByLabelText("Approved")).toBeNull();
    });

    test("the confirm dialog does not promise a live deploy while a component is blocked", () => {
        localStorage.clear();
        renderCard({ manifest: blockedManifest, isApproved: false });
        fireEvent.click(screen.getByRole("button", { name: /^Build & Activate$/i }));
        const dialog = screen.getByRole("dialog");
        expect(dialog).toHaveTextContent("Nothing is deployed until the component marked as needing input has what it needs");
        expect(dialog).not.toHaveTextContent("active immediately");
    });
});
