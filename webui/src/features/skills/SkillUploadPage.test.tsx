// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { skillKeys, skillsApi } from "@/api/skills";

import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { Skill } from "@/types";

import { SkillUploadPage } from "./SkillUploadPage";

vi.mock("@/utils/fileValidation", async importOriginal => ({ ...(await importOriginal<typeof import("@/utils/fileValidation")>()), isZipFile: async () => true }));

const skill = { id: "sk-1", name: "order-lookup", description: "Looks up customer orders.", agents: [], discoveryStatus: "ready" } as unknown as Skill;

function renderReupload() {
    const router = createMemoryRouter(
        [
            {
                path: "/skills/:id/upload",
                loader: () => ({ skillId: "sk-1" }),
                element: (
                    <StoryProvider shouldIsolateQueryClient userContextValues={{ userHasCapability: () => true }}>
                        <SkillUploadPage />
                    </StoryProvider>
                ),
            },
            { path: "*", element: <div>skill view</div> },
        ],
        { initialEntries: ["/skills/sk-1/upload"] }
    );
    render(<RouterProvider router={router} />);
}

describe("SkillUploadPage re-upload", () => {
    afterEach(() => vi.restoreAllMocks());

    it("refreshes the skill's cached detail and files, not only the lists", async () => {
        vi.spyOn(skillsApi, "get").mockResolvedValue(skill);
        const uploadZip = vi.spyOn(skillsApi, "uploadZip").mockResolvedValue(skill);
        const invalidate = vi.spyOn(QueryClient.prototype, "invalidateQueries");
        renderReupload();

        const upload = await screen.findByTestId("uploadSkillButton");
        const input = document.querySelector<HTMLInputElement>('input[type="file"][name="skillZip"]')!;
        fireEvent.change(input, { target: { files: [new File(["PK"], "order-lookup.zip", { type: "application/zip" })] } });
        await waitFor(() => expect(screen.getByText("order-lookup.zip")).toBeInTheDocument());
        await userEvent.click(upload);

        expect(await screen.findByText("skill view")).toBeInTheDocument();
        expect(uploadZip).toHaveBeenCalledWith("sk-1", expect.any(File));
        expect(invalidate).toHaveBeenCalledWith({ queryKey: skillKeys.lists() });
        expect(invalidate).toHaveBeenCalledWith({ queryKey: skillKeys.details() });
    });
});
