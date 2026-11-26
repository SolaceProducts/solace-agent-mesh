// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { TooltipProvider } from "@/ui/tooltip";

import type { ComponentCanvasData } from "./hooks/useBuilderCanvasData";
import type { BuildManifest } from "./hooks/useBuilderManifest";

describe("InlineCreationProgress", () => {
    let InlineCreationProgress: typeof import("./InlineCreationProgress").InlineCreationProgress;
    const onResume = vi.fn();

    beforeEach(async () => {
        vi.resetModules();
        onResume.mockClear();

        vi.doMock("@/providers/contexts/UIModeContext", () => ({
            useUIModeContext: () => ({ showActivityPanel: true }),
        }));

        const mod = await import("./InlineCreationProgress");
        InlineCreationProgress = mod.InlineCreationProgress;
    });

    type Comp = NonNullable<BuildManifest["components"]>[number];

    function dataFor(name: string, overrides: Partial<ComponentCanvasData> = {}): ComponentCanvasData {
        return {
            name,
            filename: `${name}.yaml`,
            type: "agent",
            yamlText: null,
            isLoading: false,
            isGenerating: false,
            isQueued: false,
            isWaitingForInput: false,
            isBuildingDeps: false,
            isBuilderResponding: false,
            dependencyProgress: [],
            ...overrides,
        };
    }

    function setup(components: Comp[], dataEntries: Array<[string, Partial<ComponentCanvasData>]> = [], overrides: Partial<BuildManifest> = {}) {
        const manifest: BuildManifest = { name: "build", components, ...overrides };
        const componentData = new Map(dataEntries.map(([n, overs]) => [n, dataFor(n, overs)]));
        return { manifest, componentData };
    }

    test("renders nothing when there are no session-origin components", () => {
        const { manifest, componentData } = setup([{ filename: "a.yaml", type: "agent", name: "A", origin: "platform" }]);
        const { container } = render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={false} />);
        expect(container.firstChild).toBeNull();
    });

    test("shows the unified 'Activity Timeline' header while components are still being built", () => {
        const { manifest, componentData } = setup(
            [
                { filename: "a.yaml", type: "agent", name: "A", status: "draft" },
                { filename: "b.yaml", type: "agent", name: "B", status: "draft" },
            ],
            [
                ["A", { isGenerating: true }],
                ["B", {}],
            ]
        );
        render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={true} />);
        screen.getByText(/Activity Timeline/i);
    });

    test("shows the 'Activity Timeline' header with a completion check when all components are done", () => {
        const { manifest, componentData } = setup(
            [
                { filename: "a.yaml", type: "agent", name: "A", status: "deployed" },
                { filename: "b.yaml", type: "agent", name: "B", status: "deployed" },
            ],
            [
                ["A", { yamlText: "ok" }],
                ["B", { yamlText: "ok" }],
            ]
        );
        render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={false} />);
        screen.getByText(/Activity Timeline/i);
    });

    test("collapses to a single button when isCollapsed prop is true", () => {
        const onCollapsedChange = vi.fn();
        const { manifest, componentData } = setup([{ filename: "a.yaml", type: "agent", name: "A", status: "deployed" }], [["A", { yamlText: "ok" }]]);
        render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={false} isCollapsed={true} onCollapsedChange={onCollapsedChange} />);

        const toggle = screen.getByRole("button", { name: /Activity Timeline/i });
        fireEvent.click(toggle);
        expect(onCollapsedChange).toHaveBeenCalledWith(false);
    });

    test("collapsed header shows a working spinner (not the completion check) while the agent is still responding", () => {
        const { manifest, componentData } = setup([{ filename: "a.yaml", type: "agent", name: "A", status: "deployed" }], [["A", { yamlText: "ok" }]]);
        const { container } = render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={true} isCollapsed={true} onCollapsedChange={vi.fn()} />);
        screen.getByRole("button", { name: /Activity Timeline/i });
        expect(container.querySelector(".animate-spin")).not.toBeNull();
    });

    test("collapsed header shows the completion check (no spinner) once done and not responding", () => {
        const { manifest, componentData } = setup([{ filename: "a.yaml", type: "agent", name: "A", status: "deployed" }], [["A", { yamlText: "ok" }]]);
        const { container } = render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={false} isCollapsed={true} onCollapsedChange={vi.fn()} />);
        screen.getByRole("button", { name: /Activity Timeline/i });
        expect(container.querySelector(".animate-spin")).toBeNull();
        expect(container.querySelector(".lucide-circle-check-big")).not.toBeNull();
    });

    test("does NOT auto-collapse while the agent is still responding (even when all components are done)", () => {
        vi.useFakeTimers();
        try {
            const onCollapsedChange = vi.fn();
            const { manifest, componentData } = setup([{ filename: "a.yaml", type: "agent", name: "A", status: "deployed" }], [["A", { yamlText: "ok" }]]);
            render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={true} isCollapsed={false} onCollapsedChange={onCollapsedChange} />);
            act(() => {
                vi.advanceTimersByTime(2000);
            });
            expect(onCollapsedChange).not.toHaveBeenCalled();
        } finally {
            vi.useRealTimers();
        }
    });

    test("auto-collapses once the agent stops responding and all components are done", () => {
        vi.useFakeTimers();
        try {
            const onCollapsedChange = vi.fn();
            const { manifest, componentData } = setup([{ filename: "a.yaml", type: "agent", name: "A", status: "deployed" }], [["A", { yamlText: "ok" }]]);
            const { rerender } = render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={true} isCollapsed={false} onCollapsedChange={onCollapsedChange} />);
            act(() => {
                vi.advanceTimersByTime(2000);
            });
            expect(onCollapsedChange).not.toHaveBeenCalled();

            rerender(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={false} isCollapsed={false} onCollapsedChange={onCollapsedChange} />);
            act(() => {
                vi.advanceTimersByTime(1600);
            });
            expect(onCollapsedChange).toHaveBeenCalledWith(true);
        } finally {
            vi.useRealTimers();
        }
    });

    test("renders streaming activity updates under the header without per-component node rows", () => {
        const { manifest, componentData } = setup(
            [
                { filename: "a.yaml", type: "agent", name: "A", status: "draft" },
                { filename: "b.yaml", type: "agent", name: "B", status: "draft" },
            ],
            [
                ["A", { isGenerating: true }],
                ["B", {}],
            ]
        );
        const activityUpdates = [{ type: "status" as const, text: "Generating A and B configs in parallel...", timestamp: 1 }];
        render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={true} activityUpdates={activityUpdates} activityActive={true} />);
        fireEvent.click(screen.getByTitle("Show all activity steps"));
        screen.getByText(/Activity Timeline/i);
        screen.getByText("Generating A and B configs in parallel...");
        expect(screen.queryByText("A")).toBeNull();
        expect(screen.queryByText("B")).toBeNull();
    });

    describe("live single-line view", () => {
        const buildingTwo = () =>
            setup(
                [
                    { filename: "a.yaml", type: "agent", name: "A", status: "draft" },
                    { filename: "b.yaml", type: "agent", name: "B", status: "draft" },
                ],
                [
                    ["A", { isGenerating: true }],
                    ["B", {}],
                ]
            );

        const threeUpdates = [
            { type: "status" as const, text: "Generating RcEditProbe agent config...", timestamp: 1 },
            { type: "status" as const, text: "Loading agent schema skill...", timestamp: 2 },
            { type: "status" as const, text: "Receiving artifact `rc_edit_probe_agent.yaml`", timestamp: 3 },
        ];

        test("collapses a streaming turn to the latest step alone", () => {
            const { manifest, componentData } = buildingTwo();
            render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={true} activityUpdates={threeUpdates} activityActive={true} />);

            screen.getByText("Receiving artifact `rc_edit_probe_agent.yaml`");
            expect(screen.queryByText("Generating RcEditProbe agent config...")).toBeNull();
            expect(screen.queryByText("Loading agent schema skill...")).toBeNull();
            expect(screen.queryByText(/Activity Timeline/i)).toBeNull();
            expect(screen.getAllByLabelText("Working")).toHaveLength(1);
        });

        test("expands to the full timeline and collapses back to the live line", () => {
            const { manifest, componentData } = buildingTwo();
            render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={true} activityUpdates={threeUpdates} activityActive={true} />);

            fireEvent.click(screen.getByTitle("Show all activity steps"));
            screen.getByText("Generating RcEditProbe agent config...");
            screen.getByText("Loading agent schema skill...");

            fireEvent.click(screen.getByRole("button", { name: /Activity Timeline/i }));
            expect(screen.queryByText("Generating RcEditProbe agent config...")).toBeNull();
            screen.getByTitle("Show all activity steps");
        });

        test("announces the expanded state on the header control", () => {
            const { manifest, componentData } = buildingTwo();
            render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={true} activityUpdates={threeUpdates} activityActive={true} />);

            expect(screen.getByTitle("Show all activity steps")).toHaveAttribute("aria-expanded", "false");

            fireEvent.click(screen.getByTitle("Show all activity steps"));
            expect(screen.getByRole("button", { name: /Activity Timeline/i })).toHaveAttribute("aria-expanded", "true");
        });

        test("expanding and collapsing never reports the block as collapsed", () => {
            const onCollapsedChange = vi.fn();
            const { manifest, componentData } = buildingTwo();
            render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={true} isCollapsed={false} onCollapsedChange={onCollapsedChange} activityUpdates={threeUpdates} activityActive={true} />);

            fireEvent.click(screen.getByTitle("Show all activity steps"));
            fireEvent.click(screen.getByRole("button", { name: /Activity Timeline/i }));

            expect(onCollapsedChange.mock.calls.flat()).not.toContain(true);
        });

        test("collapsing an expanded live line never collapses the block, even once every component has settled", () => {
            const onCollapsedChange = vi.fn();
            const { manifest, componentData } = setup(
                [
                    { filename: "a.yaml", type: "agent", name: "A", status: "deployed" },
                    { filename: "b.yaml", type: "agent", name: "B", status: "deployed" },
                ],
                [
                    ["A", { yamlText: "ok" }],
                    ["B", { yamlText: "ok" }],
                ]
            );
            render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={true} isCollapsed={false} onCollapsedChange={onCollapsedChange} activityUpdates={threeUpdates} activityActive={true} />);

            fireEvent.click(screen.getByTitle("Show all activity steps"));
            fireEvent.click(screen.getByRole("button", { name: /Activity Timeline/i }));

            expect(onCollapsedChange.mock.calls.flat()).not.toContain(true);
            screen.getByTitle("Show all activity steps");
        });

        test("keeps the 'Waiting for input' cue on the live line", () => {
            const { manifest, componentData } = setup([{ filename: "a.yaml", type: "connector", name: "A", status: "draft" }], [["A", { isWaitingForInput: true }]]);
            render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={true} activityUpdates={threeUpdates} activityActive={true} />);

            screen.getByText("Receiving artifact `rc_edit_probe_agent.yaml`");
            screen.getByText("Waiting for input");
        });

        test("an interrupted build keeps the full header so Resume build stays prominent", () => {
            const { manifest, componentData } = setup(
                [
                    { filename: "a.yaml", type: "agent", name: "A", status: "draft" },
                    { filename: "b.yaml", type: "agent", name: "B", status: "draft" },
                ],
                [
                    ["A", { yamlText: "ok" }],
                    ["B", {}],
                ]
            );
            render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={false} activityUpdates={threeUpdates} activityActive={true} onResume={onResume} />);

            screen.getByRole("button", { name: /Resume build/i });
            screen.getByText(/Activity Timeline/i);
            expect(screen.queryByTitle("Show all activity steps")).toBeNull();
        });

        describe("when the host reports the build was cut short (Stop or an error)", () => {
            const oneDraft = () => setup([{ filename: "a.yaml", type: "agent", name: "A", status: "draft" }], [["A", {}]]);
            const renderCutShort = (over: Partial<React.ComponentProps<typeof InlineCreationProgress>> = {}, entries: ReturnType<typeof oneDraft> = oneDraft()) =>
                render(<InlineCreationProgress manifest={entries.manifest} componentData={entries.componentData} isResponding={false} activityUpdates={threeUpdates} activityActive={false} isInterrupted onResume={onResume} {...over} />);

            test("says so, offers Resume build, and its last step reads stopped", () => {
                renderCutShort();

                screen.getByText("Build interrupted");
                screen.getByRole("button", { name: /Resume build/i });
                screen.getByText("Receiving artifact `rc_edit_probe_agent.yaml` (interrupted)");
                expect(screen.queryByText("Receiving artifact `rc_edit_probe_agent.yaml`")).toBeNull();
            });

            test("its Resume build runs the host's handler", () => {
                renderCutShort();

                fireEvent.click(screen.getByRole("button", { name: /Resume build/i }));

                expect(onResume).toHaveBeenCalledTimes(1);
            });

            test("earlier steps read as finished and only the last one as interrupted", () => {
                renderCutShort();

                screen.getByText("Generating RcEditProbe agent config");
                screen.getByText("Receiving artifact `rc_edit_probe_agent.yaml` (interrupted)");
            });

            test("leaves Resume build to the plan card when it hosts that action, keeping the status", () => {
                renderCutShort({ onResume: undefined });

                screen.getByText("Build interrupted");
                expect(screen.queryByRole("button", { name: /Resume build/i })).toBeNull();
            });

            describe("with every row holding streamed config text (so every row reads as done)", () => {
                const everyRowDone = () => setup([{ filename: "a.yaml", type: "agent", name: "A", status: "draft" }], [["A", { yamlText: "name: A\nhalf" }]]);

                afterEach(() => vi.useRealTimers());

                test("shows no completion check and no collapse control, as an unsettled build", () => {
                    const { container } = renderCutShort({}, everyRowDone());

                    expect(container.querySelector(".lucide-circle-check-big")).toBeNull();
                    expect(screen.queryByRole("button", { name: /^Activity Timeline$/i })).toBeNull();
                    screen.getByText("Build interrupted");
                });

                test("does not collapse itself, so the status and Resume build stay on screen", () => {
                    vi.useFakeTimers();
                    renderCutShort({}, everyRowDone());

                    act(() => {
                        vi.advanceTimersByTime(2000);
                    });

                    screen.getByRole("button", { name: /Resume build/i });
                    screen.getByText("Build interrupted");
                });

                test("a finished build with the same rows still collapses itself", () => {
                    vi.useFakeTimers();
                    const done = setup([{ filename: "a.yaml", type: "agent", name: "A", status: "validated" }], [["A", { yamlText: "ok" }]]);
                    const onCollapsedChange = vi.fn();
                    render(<InlineCreationProgress manifest={done.manifest} componentData={done.componentData} isResponding={false} activityUpdates={threeUpdates} activityActive={false} onCollapsedChange={onCollapsedChange} />);

                    act(() => {
                        vi.advanceTimersByTime(2000);
                    });

                    expect(onCollapsedChange).toHaveBeenCalledWith(true);
                });
            });

            test("a failed component's 'Fixing errors' label stays ahead of 'Build interrupted'", () => {
                const mixed = setup(
                    [
                        { filename: "a.yaml", type: "agent", name: "A", status: "error" },
                        { filename: "b.yaml", type: "agent", name: "B", status: "draft" },
                    ],
                    [
                        ["A", {}],
                        ["B", {}],
                    ]
                );
                renderCutShort({}, mixed);

                screen.getByText("Fixing errors");
                expect(screen.queryByText("Build interrupted")).toBeNull();
            });

            test("a still-active last message does not leave the last step spinning", () => {
                const { container } = renderCutShort({ activityActive: true });

                expect(container.querySelector(".animate-spin")).toBeNull();
            });
        });

        test("a live build keeps its last step spinning once the steps are expanded", () => {
            const { manifest, componentData } = setup([{ filename: "a.yaml", type: "agent", name: "A", status: "draft" }], [["A", { isGenerating: true }]]);
            const { container } = render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={true} activityUpdates={threeUpdates} activityActive={true} />);

            fireEvent.click(screen.getByTitle("Show all activity steps"));

            expect(container.querySelector(".animate-spin")).not.toBeNull();
        });

        test("the fallback keeps the wording of its steps as written", () => {
            const { manifest, componentData } = setup(
                [
                    { filename: "a.yaml", type: "agent", name: "A", status: "draft" },
                    { filename: "b.yaml", type: "agent", name: "B", status: "draft" },
                ],
                [
                    ["A", { yamlText: "ok" }],
                    ["B", {}],
                ]
            );
            render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={false} activityUpdates={threeUpdates} activityActive={false} onResume={onResume} />);

            screen.getByText("Generating RcEditProbe agent config...");
        });

        test("a finished build strips the ellipsis of its steps", () => {
            const done = setup([{ filename: "a.yaml", type: "agent", name: "A", status: "validated" }], [["A", { yamlText: "ok" }]]);
            render(<InlineCreationProgress manifest={done.manifest} componentData={done.componentData} isResponding={false} activityUpdates={threeUpdates} activityActive={false} />);

            screen.getByText("Generating RcEditProbe agent config");
        });

        test("the fallback with a still-active last message does not leave the last step spinning", () => {
            const { manifest, componentData } = setup(
                [
                    { filename: "a.yaml", type: "agent", name: "A", status: "draft" },
                    { filename: "b.yaml", type: "agent", name: "B", status: "draft" },
                ],
                [
                    ["A", { yamlText: "ok" }],
                    ["B", {}],
                ]
            );
            const { container } = render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={false} activityUpdates={threeUpdates} activityActive={true} onResume={onResume} />);

            expect(container.querySelector(".animate-spin")).toBeNull();
        });

        test("the fallback (a finished component, another pending) stays quiet while the agent is still responding", () => {
            const { manifest, componentData } = setup(
                [
                    { filename: "a.yaml", type: "agent", name: "A", status: "draft" },
                    { filename: "b.yaml", type: "agent", name: "B", status: "draft" },
                ],
                [
                    ["A", { yamlText: "ok" }],
                    ["B", {}],
                ]
            );
            render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={true} activityUpdates={threeUpdates} activityActive={true} />);

            expect(screen.queryByText("Build interrupted")).toBeNull();
            expect(screen.queryByRole("button", { name: /Resume build/i })).toBeNull();
        });

        test("without the host's verdict, a build with nothing written is not called interrupted", () => {
            const { manifest, componentData } = setup([{ filename: "a.yaml", type: "agent", name: "A", status: "draft" }], [["A", {}]]);
            render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={false} activityUpdates={threeUpdates} activityActive={false} />);

            expect(screen.queryByText("Build interrupted")).toBeNull();
            expect(screen.queryByText(/\(interrupted\)/)).toBeNull();
            expect(screen.queryByRole("button", { name: /Resume build/i })).toBeNull();
        });

        test("a finished component with another pending, nothing responding and no verdict from the host keeps its own Resume build, without marking a step", () => {
            const { manifest, componentData } = setup(
                [
                    { filename: "a.yaml", type: "agent", name: "A", status: "draft" },
                    { filename: "b.yaml", type: "agent", name: "B", status: "draft" },
                ],
                [
                    ["A", { yamlText: "ok" }],
                    ["B", {}],
                ]
            );
            render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={false} activityUpdates={threeUpdates} activityActive={false} onResume={onResume} />);

            screen.getByText("Build interrupted");
            screen.getByRole("button", { name: /Resume build/i });
            expect(screen.queryByText(/\(interrupted\)/)).toBeNull();
        });

        test("a workflow validated while its only child failed, with nothing responding, keeps Resume build even though no row reads done", () => {
            const { manifest, componentData } = setup(
                [
                    { filename: "w.yaml", type: "workflow", name: "W", status: "validated", nodes: [{ id: "n1", type: "agent", agent_name: "A" }] },
                    { filename: "a.yaml", type: "agent", name: "A", status: "error" },
                ],
                [
                    ["W", { type: "workflow", status: "validated", yamlText: "ok" }],
                    ["A", { status: "error", yamlText: "failed" }],
                ]
            );
            render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={false} activityUpdates={threeUpdates} activityActive={false} onResume={onResume} />);

            screen.getByRole("button", { name: /Resume build/i });
        });

        test("the builder responding, whatever the entries say, never offers Resume build", () => {
            const { manifest, componentData } = setup(
                [
                    { filename: "a.yaml", type: "agent", name: "A", status: "validated" },
                    { filename: "b.yaml", type: "agent", name: "B", status: "draft" },
                ],
                [
                    ["A", { status: "validated", yamlText: "ok", isBuilderResponding: false }],
                    ["B", { status: "draft", yamlText: "ok", isBuilderResponding: false }],
                ]
            );
            render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={true} activityUpdates={threeUpdates} activityActive={true} onResume={onResume} />);

            expect(screen.queryByRole("button", { name: /Resume build/i })).toBeNull();
            expect(screen.queryByText("Build interrupted")).toBeNull();
        });

        test("a timeline the user collapsed expands again when the build turns out to be interrupted", () => {
            const failed = setup(
                [
                    { filename: "a.yaml", type: "agent", name: "A", status: "error" },
                    { filename: "b.yaml", type: "agent", name: "B", status: "error" },
                ],
                [
                    ["A", { status: "error", yamlText: "failed" }],
                    ["B", { status: "error", yamlText: "failed" }],
                ]
            );
            const onCollapsedChange = vi.fn();
            const view = render(
                <InlineCreationProgress
                    manifest={failed.manifest}
                    componentData={failed.componentData}
                    isResponding={false}
                    activityUpdates={threeUpdates}
                    activityActive={false}
                    isCollapsed={true}
                    onCollapsedChange={onCollapsedChange}
                    onResume={onResume}
                />
            );
            onCollapsedChange.mockClear();

            const stopped = setup(
                [
                    { filename: "a.yaml", type: "agent", name: "A", status: "error" },
                    { filename: "b.yaml", type: "agent", name: "B", status: "validated" },
                ],
                [
                    ["A", { status: "error", yamlText: "failed" }],
                    ["B", { status: "validated", yamlText: "ok" }],
                ]
            );
            view.rerender(
                <InlineCreationProgress
                    manifest={stopped.manifest}
                    componentData={stopped.componentData}
                    isResponding={false}
                    activityUpdates={threeUpdates}
                    activityActive={false}
                    isCollapsed={true}
                    onCollapsedChange={onCollapsedChange}
                    onResume={onResume}
                />
            );

            expect(onCollapsedChange).toHaveBeenCalledWith(false);
        });

        test("a platform agent that is already live does not make a build with nothing written read as interrupted", () => {
            const { manifest, componentData } = setup(
                [
                    { filename: "p.yaml", type: "agent", name: "P", status: "deployed", origin: "platform" },
                    { filename: "a.yaml", type: "agent", name: "A", status: "draft" },
                ],
                [
                    ["P", { status: "deployed", origin: "platform" }],
                    ["A", { status: "draft" }],
                ]
            );
            render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={false} activityUpdates={threeUpdates} activityActive={false} onResume={onResume} />);

            expect(screen.queryByText("Build interrupted")).toBeNull();
            expect(screen.queryByRole("button", { name: /Resume build/i })).toBeNull();
        });

        test("a build with every component validated and written is neither interrupted nor offered Resume build", () => {
            const { manifest, componentData } = setup(
                [
                    { filename: "a.yaml", type: "agent", name: "A", status: "validated" },
                    { filename: "b.yaml", type: "agent", name: "B", status: "validated" },
                ],
                [
                    ["A", { status: "validated", yamlText: "ok" }],
                    ["B", { status: "validated", yamlText: "ok" }],
                ]
            );
            render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={false} activityUpdates={threeUpdates} activityActive={false} onResume={onResume} />);

            expect(screen.queryByText("Build interrupted")).toBeNull();
            expect(screen.queryByRole("button", { name: /Resume build/i })).toBeNull();
        });

        test("a finished component beside a failed one, nothing responding, keeps Resume build as the way to retry", () => {
            const { manifest, componentData } = setup(
                [
                    { filename: "a.yaml", type: "agent", name: "A", status: "validated" },
                    { filename: "b.yaml", type: "agent", name: "B", status: "error" },
                ],
                [
                    ["A", { status: "validated", yamlText: "ok" }],
                    ["B", { status: "error", yamlText: "failed" }],
                ]
            );
            render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={false} activityUpdates={threeUpdates} activityActive={false} onResume={onResume} />);

            screen.getByRole("button", { name: /Resume build/i });
            expect(screen.queryByRole("button", { name: /Activity Timeline/i })).toBeNull();
        });

        test.each([
            ["a draft that kept its YAML", "draft"],
            ["a component with no status", undefined],
        ])("%s beside a validated one keeps Resume build on screen instead of collapsing to success", (_label, status) => {
            vi.useFakeTimers();
            const { manifest, componentData } = setup(
                [
                    { filename: "a.yaml", type: "agent", name: "A", status: "validated" },
                    { filename: "b.yaml", type: "agent", name: "B", status },
                ],
                [
                    ["A", { status: "validated", yamlText: "ok" }],
                    ["B", { status, yamlText: "written" }],
                ]
            );
            const onCollapsedChange = vi.fn();
            render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={false} activityUpdates={threeUpdates} activityActive={false} onResume={onResume} onCollapsedChange={onCollapsedChange} />);

            act(() => {
                vi.advanceTimersByTime(2000);
            });

            screen.getByRole("button", { name: /Resume build/i });
            expect(onCollapsedChange).not.toHaveBeenCalledWith(true);
        });

        test.each([
            ["no other component finished", false],
            ["another component validated", true],
        ])("an open connector prompt with the builder idle and %s does not offer Resume build above it", (_label, siblingValidated) => {
            const { manifest, componentData } = setup(
                [
                    { filename: "w.yaml", type: "workflow", name: "W", status: "validated", nodes: [{ id: "n1", type: "agent", agent_name: "A" }] },
                    { filename: "a.yaml", type: "agent", name: "A", status: "draft" },
                    { filename: "k.json", type: "connector", name: "K", status: "draft" },
                    ...(siblingValidated ? [{ filename: "b.yaml", type: "agent", name: "B", status: "validated" }] : []),
                ],
                [
                    ["W", { type: "workflow", status: "validated", yamlText: "ok" }],
                    ["A", { status: "draft" }],
                    ["K", { type: "connector", status: "draft", isWaitingForInput: true }],
                    ...(siblingValidated ? ([["B", { status: "validated", yamlText: "ok" }]] as Array<[string, Partial<ComponentCanvasData>]>) : []),
                ]
            );
            render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={false} activityUpdates={threeUpdates} activityActive={false} onResume={onResume} />);

            expect(screen.queryByRole("button", { name: /Resume build/i })).toBeNull();
        });

        test("renders no live line before the first status update lands", () => {
            const { manifest, componentData } = buildingTwo();
            const { container } = render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={true} activityUpdates={[]} activityActive={true} />);

            screen.getByText(/Activity Timeline/i);
            expect(screen.queryByLabelText("Working")).toBeNull();
            expect(container.querySelector(".animate-spin")).not.toBeNull();
        });

        test("hover-reveals the activity button on the live line", () => {
            const { manifest, componentData } = buildingTwo();
            const { container } = render(
                <TooltipProvider>
                    <InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={true} activityUpdates={threeUpdates} activityActive={true} onViewWorkflow={vi.fn()} />
                </TooltipProvider>
            );

            const activityButton = container.querySelector('[data-testid="viewActivity"]');
            expect(activityButton).not.toBeNull();
            expect(activityButton).toHaveClass("opacity-0", "group-hover:opacity-100", "focus-visible:opacity-100");
        });

        test("a settled turn shows the full row list, not the live line", () => {
            const { manifest, componentData } = buildingTwo();
            render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={false} activityUpdates={threeUpdates} activityActive={false} />);

            expect(screen.queryByTitle("Show all activity steps")).toBeNull();
            screen.getByText("Generating RcEditProbe agent config...");
            screen.getByText("Receiving artifact `rc_edit_probe_agent.yaml`");
        });
    });

    test("de-duplicates workflow-owned agents without rendering node rows", () => {
        const { manifest, componentData } = setup(
            [
                {
                    filename: "wf.yaml",
                    type: "workflow",
                    name: "MyFlow",
                    status: "draft",
                    nodes: [{ id: "n1", type: "agent", agent_name: "Worker" }],
                },
                { filename: "worker.yaml", type: "agent", name: "Worker", status: "draft" },
            ],
            [
                ["MyFlow", { type: "workflow" }],
                ["Worker", {}],
            ]
        );

        render(<InlineCreationProgress manifest={manifest} componentData={componentData} isResponding={true} />);

        screen.getByText(/Activity Timeline/i);
        expect(screen.queryByText("Worker")).toBeNull();
        expect(screen.queryByText("MyFlow")).toBeNull();
    });
});
