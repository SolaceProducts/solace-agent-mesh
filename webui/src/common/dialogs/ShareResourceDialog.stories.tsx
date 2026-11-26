// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import type { Meta } from "@storybook/react-vite";
import { Eye, Pencil, Trash2 } from "lucide-react";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent } from "storybook/test";

import { Button } from "@/ui/button";

import { paginatedResponse, withDefaults } from "@/testing/helpers";

import { type AccessLevelOption, ShareResourceDialog, type ShareResourceEntry } from "./ShareResourceDialog";

type AccessLevel = "resource_viewer" | "resource_editor";

const ACCESS_LEVELS: AccessLevelOption<AccessLevel>[] = [
    { value: "resource_viewer", label: "Viewer", icon: <Eye className="h-4 w-4" />, description: "Can use the most active version" },
    { value: "resource_editor", label: "Editor", icon: <Pencil className="h-4 w-4" />, description: "Can update and create new versions" },
];

interface MockPerson {
    id: string;
    workEmail: string;
    displayName: string;
}

const DIRECTORY: MockPerson[] = [
    { id: "p1", workEmail: "alice@example.org", displayName: "Alice Chen" },
    { id: "p2", workEmail: "alex@example.org", displayName: "Alex Park" },
    { id: "p3", workEmail: "bob@example.org", displayName: "Bob Iyer" },
    { id: "p4", workEmail: "bella@example.org", displayName: "Bella Cruz" },
    { id: "p5", workEmail: "charlie@example.org", displayName: "Charlie Khan" },
    { id: "p6", workEmail: "dana@example.org", displayName: "Dana Smith" },
    { id: "p7", workEmail: "ethan@example.org", displayName: "Ethan Wu" },
    { id: "p8", workEmail: "fatima@example.org", displayName: "Fatima Ali" },
    { id: "p9", workEmail: "owner@example.org", displayName: "Olivia Owner" },
    { id: "p10", workEmail: "you@example.org", displayName: "You (Tester)" },
];

const peopleSearchHandler = http.get("*/api/v1/people/search", ({ request }) => {
    const url = new URL(request.url);
    const q = (url.searchParams.get("q") ?? "").toLowerCase();
    const matches = q.length === 0 ? [] : DIRECTORY.filter(p => p.workEmail.toLowerCase().includes(q) || p.displayName.toLowerCase().includes(q));
    return HttpResponse.json(paginatedResponse(matches));
});

const meta = {
    title: "Common/ShareResourceDialog",
    component: ShareResourceDialog,
    parameters: {
        layout: "fullscreen",
        msw: { handlers: withDefaults(peopleSearchHandler) },
        docs: {
            description: {
                component:
                    "Resource share dialog used by prompt template sharing (and any future shareable resource). Seeds an empty AddGroup on open and autofocuses its input so a multi-recipient flow is type → Enter → type → Enter with no extra clicks.",
            },
        },
    },
} satisfies Meta<typeof ShareResourceDialog>;

export default meta;

interface PlaygroundConfig {
    identityMode: "plain" | "typeahead";
    ownerEmail: string | null;
    initialShares: ShareResourceEntry<AccessLevel>[];
    resourceLabel: string;
    resourceName: string;
}

interface ApiLogEntry {
    id: number;
    kind: "onCreate" | "onReplace" | "onSuccess";
    payload: unknown;
    at: string;
}

function nextId(): number {
    return Math.floor(Math.random() * 1_000_000_000);
}

function PlaygroundStory() {
    const [config, setConfig] = useState<PlaygroundConfig>({
        identityMode: "typeahead",
        ownerEmail: "owner@example.org",
        initialShares: [
            { id: "seed-1", userEmail: "alice@example.org", accessLevel: "resource_viewer" },
            { id: "seed-2", userEmail: "bob@example.org", accessLevel: "resource_editor" },
        ],
        resourceLabel: "Prompt",
        resourceName: "Weekly Status Report Generator",
    });
    const [isOpen, setIsOpen] = useState(true);
    const [shareDraftEmail, setShareDraftEmail] = useState<string>(DIRECTORY[2].workEmail);
    const [shareDraftLevel, setShareDraftLevel] = useState<AccessLevel>("resource_viewer");
    const [log, setLog] = useState<ApiLogEntry[]>([]);

    const recordLog = (kind: ApiLogEntry["kind"], payload: unknown) => {
        const stamp = new Date().toLocaleTimeString();
        setLog(prev => [{ id: nextId(), kind, payload, at: stamp }, ...prev].slice(0, 20));
    };

    const addSeededShare = () => {
        if (config.initialShares.some(s => s.userEmail === shareDraftEmail)) return;
        if (shareDraftEmail === config.ownerEmail) return;
        setConfig(prev => ({
            ...prev,
            initialShares: [...prev.initialShares, { id: `seed-${nextId()}`, userEmail: shareDraftEmail, accessLevel: shareDraftLevel }],
        }));
    };

    const removeSeededShare = (email: string) => {
        setConfig(prev => ({ ...prev, initialShares: prev.initialShares.filter(s => s.userEmail !== email) }));
    };

    const reset = () => {
        setLog([]);
        setIsOpen(true);
    };

    return (
        <div className="grid h-screen grid-cols-[360px_1fr_360px] gap-4 bg-(--background-w20) p-6">
            <aside className="flex flex-col gap-4 overflow-y-auto rounded-md border border-(--secondary-w40) bg-(--background-w10) p-4">
                <header>
                    <h2 className="text-sm font-semibold text-(--primary-text-wMain)">Playground</h2>
                    <p className="mt-1 text-xs text-(--secondary-text-wMain)">Dial the inputs and watch the dialog react.</p>
                </header>

                <fieldset className="flex flex-col gap-2">
                    <legend className="text-xs font-medium text-(--secondary-text-wMain)">Identity mode</legend>
                    <div className="flex gap-2">
                        <Button variant={config.identityMode === "plain" ? "outline" : "ghost"} size="sm" onClick={() => setConfig(p => ({ ...p, identityMode: "plain" }))}>
                            PlainEmailInput
                        </Button>
                        <Button variant={config.identityMode === "typeahead" ? "outline" : "ghost"} size="sm" onClick={() => setConfig(p => ({ ...p, identityMode: "typeahead" }))}>
                            UserMultiTypeahead
                        </Button>
                    </div>
                    <p className="text-xs text-(--secondary-text-wMain)">
                        {config.identityMode === "plain"
                            ? "identityServiceType=null. Free-form email entry, Enter commits."
                            : 'identityServiceType="noop". Typeahead-backed by the mock directory below; free-entry still works for emails not in the list.'}
                    </p>
                </fieldset>

                <fieldset className="flex flex-col gap-2">
                    <legend className="text-xs font-medium text-(--secondary-text-wMain)">Resource owner</legend>
                    <select className="rounded-xs border border-(--secondary-w40) bg-transparent px-2 py-1 text-sm" value={config.ownerEmail ?? ""} onChange={e => setConfig(p => ({ ...p, ownerEmail: e.target.value === "" ? null : e.target.value }))}>
                        <option value="">(no owner row)</option>
                        {DIRECTORY.map(p => (
                            <option key={p.id} value={p.workEmail}>{`${p.displayName} <${p.workEmail}>`}</option>
                        ))}
                    </select>
                </fieldset>

                <fieldset className="flex flex-col gap-2">
                    <legend className="text-xs font-medium text-(--secondary-text-wMain)">Pre-seeded existing shares</legend>
                    {config.initialShares.length === 0 && <p className="text-xs text-(--secondary-text-wMain) italic">none — dialog will render only the seeded add-row.</p>}
                    {config.initialShares.map(s => (
                        <div key={s.id} className="flex items-center gap-2 text-sm">
                            <span className="flex-1 truncate" title={s.userEmail}>
                                {s.userEmail}
                            </span>
                            <span className="text-xs text-(--secondary-text-wMain)">{s.accessLevel === "resource_viewer" ? "Viewer" : "Editor"}</span>
                            <Button variant="ghost" size="sm" onClick={() => removeSeededShare(s.userEmail)} aria-label={`Remove pre-seed ${s.userEmail}`}>
                                <Trash2 className="h-3.5 w-3.5" />
                            </Button>
                        </div>
                    ))}
                    <div className="flex flex-col gap-1 rounded-md border border-dashed border-(--secondary-w40) p-2">
                        <select className="rounded-xs border border-(--secondary-w40) bg-transparent px-2 py-1 text-sm" value={shareDraftEmail} onChange={e => setShareDraftEmail(e.target.value)}>
                            {DIRECTORY.filter(p => p.workEmail !== config.ownerEmail && !config.initialShares.some(s => s.userEmail === p.workEmail)).map(p => (
                                <option key={p.id} value={p.workEmail}>{`${p.displayName} <${p.workEmail}>`}</option>
                            ))}
                        </select>
                        <select className="rounded-xs border border-(--secondary-w40) bg-transparent px-2 py-1 text-sm" value={shareDraftLevel} onChange={e => setShareDraftLevel(e.target.value as AccessLevel)}>
                            <option value="resource_viewer">Viewer</option>
                            <option value="resource_editor">Editor</option>
                        </select>
                        <Button variant="outline" size="sm" onClick={addSeededShare}>
                            Add pre-seeded share
                        </Button>
                    </div>
                </fieldset>

                <fieldset className="flex flex-col gap-2">
                    <legend className="text-xs font-medium text-(--secondary-text-wMain)">Resource</legend>
                    <input
                        className="rounded-xs border border-(--secondary-w40) bg-transparent px-2 py-1 text-sm"
                        value={config.resourceLabel}
                        onChange={e => setConfig(p => ({ ...p, resourceLabel: e.target.value }))}
                        aria-label="Resource label"
                        placeholder="resourceLabel"
                    />
                    <input
                        className="rounded-xs border border-(--secondary-w40) bg-transparent px-2 py-1 text-sm"
                        value={config.resourceName}
                        onChange={e => setConfig(p => ({ ...p, resourceName: e.target.value }))}
                        aria-label="Resource name"
                        placeholder="resourceName"
                    />
                </fieldset>

                <div className="flex gap-2">
                    <Button variant="outline" size="sm" onClick={reset}>
                        Reset log
                    </Button>
                    {!isOpen && (
                        <Button variant="outline" size="sm" onClick={() => setIsOpen(true)}>
                            Reopen dialog
                        </Button>
                    )}
                </div>
            </aside>

            <section className="flex items-center justify-center">
                <ShareResourceDialog<AccessLevel>
                    key={`${config.identityMode}-${config.ownerEmail ?? "none"}-${config.initialShares.map(s => s.id).join(",")}`}
                    isOpen={isOpen}
                    onClose={() => setIsOpen(false)}
                    resourceLabel={config.resourceLabel || "Resource"}
                    resourceName={config.resourceName || "Untitled"}
                    ownerEmail={config.ownerEmail ?? undefined}
                    shares={config.initialShares}
                    identityServiceType={config.identityMode === "plain" ? null : "noop"}
                    accessLevels={ACCESS_LEVELS}
                    onCreate={async entries => recordLog("onCreate", entries)}
                    onReplace={async entries => recordLog("onReplace", entries)}
                    onSuccess={summary => recordLog("onSuccess", summary)}
                />
            </section>

            <aside className="flex flex-col gap-2 overflow-y-auto rounded-md border border-(--secondary-w40) bg-(--background-w10) p-4">
                <header className="flex items-center justify-between">
                    <h2 className="text-sm font-semibold text-(--primary-text-wMain)">API call log</h2>
                    <span className="text-xs text-(--secondary-text-wMain)">{log.length} entries</span>
                </header>
                <p className="text-xs text-(--secondary-text-wMain)">What the dialog would POST when you click Save. Most-recent first.</p>
                {log.length === 0 && <p className="mt-2 text-xs text-(--secondary-text-wMain) italic">No calls yet. Add an email + Enter + click Save.</p>}
                {log.map(entry => (
                    <div key={entry.id} className="rounded-md border border-(--secondary-w40) p-2 text-xs">
                        <div className="flex justify-between text-(--secondary-text-wMain)">
                            <span className="font-mono">{entry.kind}</span>
                            <span>{entry.at}</span>
                        </div>
                        <pre className="mt-1 overflow-x-auto break-all whitespace-pre-wrap">{JSON.stringify(entry.payload, null, 2)}</pre>
                    </div>
                ))}
            </aside>
        </div>
    );
}

export const Playground = {
    render: () => <PlaygroundStory />,
    parameters: {
        docs: {
            description: {
                story: "Interactive harness. Use the LEFT panel to toggle identity mode (PlainEmailInput vs UserMultiTypeahead), pick the owner from the mock directory, pre-seed existing shares, and edit the resource label/name. The dialog renders in the center. The RIGHT panel logs every onCreate/onReplace/onSuccess payload that would hit the backend, so you can see exactly what would be sent.",
            },
        },
    },
};

function ClickSelectHarness() {
    const [isOpen, setIsOpen] = useState(true);
    return (
        <ShareResourceDialog<AccessLevel>
            isOpen={isOpen}
            onClose={() => setIsOpen(false)}
            resourceLabel="Project"
            resourceName="My Project"
            shares={[]}
            identityServiceType="noop"
            accessLevels={ACCESS_LEVELS}
            onCreate={async () => {}}
            onReplace={async () => {}}
        />
    );
}

export const ClickSelectsUser = {
    render: () => <ClickSelectHarness />,
    parameters: {
        docs: { description: { story: "The UserMultiTypeahead result option is selected by a plain mouse click, not just Enter." } },
    },
    play: async () => {
        const user = userEvent.setup();
        await user.click(await screen.findByRole("button", { name: /^add$/i }));
        const input = await screen.findByPlaceholderText(/search by email/i);
        await user.type(input, "alice");
        const option = await screen.findByText("alice@example.org");

        await user.pointer({ keys: "[MouseLeft>]", target: option });
        expect(document.activeElement).toBe(input);
        await user.pointer({ keys: "[/MouseLeft]", target: option });

        await user.click(option);
        expect(await screen.findByLabelText(/remove alice@example.org/i)).toBeInTheDocument();
    },
};
