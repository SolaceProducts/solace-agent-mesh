// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import { withDefaults } from "@/testing/helpers";

import { SkillBuildGuide } from "./SkillBuildGuide";

const DISMISS_KEY = "sam-skill-build-guide-dismissed";
const resetDismiss = () => {
    if (typeof window !== "undefined") window.localStorage.removeItem(DISMISS_KEY);
};

const buildTargetMixed = http.get("*/api/v2/platform/toolsetBuildTarget", () =>
    HttpResponse.json({ message: "platform reports a mixed STR fleet", errorId: "00000000-0000-0000-0000-000000000000", validationDetails: { targets: ["linux/arm64", "linux/amd64"] } }, { status: 409 })
);

const meta = {
    title: "Skills/SkillBuildGuide",
    component: SkillBuildGuide,
    decorators: [
        Story => (
            <div className="max-w-88">
                <Story />
            </div>
        ),
    ],
    beforeEach: resetDismiss,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component:
                    "Right-rail learning card that walks an author through producing an uploadable skill bundle with the CLI (scaffold → validate → package → upload). Wraps the shared `LearningBanner` shell — dismissal persists per-user in localStorage. Packaging cross-compiles bundled Go tools and builds native wheels for bundled Python tools against the deployment's build target, including its Python version (skills reuse the toolset buildTarget endpoint).",
            },
        },
    },
} satisfies Meta<typeof SkillBuildGuide>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByTestId("skillInitCommand")).toHaveTextContent("sam skill init <name>");
        expect(canvas.getByTestId("skillValidateCommand")).toHaveTextContent("sam skill validate <name>");

        await canvas.findByText(/SAM_TOOL_TARGET_OS=linux/);
        const pkg = canvas.getByTestId("skillPackageCommand");
        expect(pkg.textContent).toContain("SAM_TOOL_TARGET_OS=linux");
        expect(pkg.textContent).toContain("SAM_TOOL_TARGET_ARCH=arm64");
        expect(pkg.textContent).toContain("SAM_TOOL_PYTHON_VERSION=3.11");
        expect(pkg.textContent).toContain("sam skill package <name>");

        expect(await canvas.findByTestId("skillPythonNote")).toHaveTextContent("3.11");
    },
};

export const DetectFailed: Story = {
    parameters: {
        msw: { handlers: withDefaults(buildTargetMixed) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText(/Couldn't detect the build target/)).toBeInTheDocument();
        const pkg = canvas.getByTestId("skillPackageCommand");
        expect(pkg.textContent).toContain("SAM_TOOL_TARGET_OS=<os>");
        expect(pkg.textContent).toContain("SAM_TOOL_TARGET_ARCH=<arch>");
        expect(pkg.textContent).toContain("sam skill package <name>");
    },
};
