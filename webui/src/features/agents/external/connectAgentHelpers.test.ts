// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { ApiV2Error } from "@/api/common";
import { deployRemoteAgentV2 } from "@/api/remote-agents/v2";

import { applyValidationErrorsToForms, buildConnectSuccessMessage, buildWellKnownUrl, isDuplicateNameError, mapBadRequestOrConflict, mapConnectError, routeValidationFieldError, runDeployAfterSave, STEP1_FIELDS } from "./connectAgentHelpers";

function makeFormMethods() {
    return { setError: vi.fn() } as any;
}
function makeStepper(currentId: string = "additional-info") {
    return { current: { id: currentId }, goTo: vi.fn() };
}

describe("isDuplicateNameError", () => {
    test.each(["duplicate agent name", "an agent with this name already exists", "unique constraint violated", "idx_a2a_agents_name_normalized"])("detects %s", detail => {
        expect(isDuplicateNameError(detail)).toBe(true);
    });

    test("returns false for unrelated detail strings", () => {
        expect(isDuplicateNameError("invalid agentUrl format")).toBe(false);
        expect(isDuplicateNameError("")).toBe(false);
    });
});

describe("STEP1_FIELDS", () => {
    test("contains the step-1 form field names", () => {
        expect(STEP1_FIELDS.has("agentUrl")).toBe(true);
        expect(STEP1_FIELDS.has("agentCardLocation")).toBe(true);
        expect(STEP1_FIELDS.has("authenticationType")).toBe(true);
    });
    test("does not contain step-2 field names", () => {
        expect(STEP1_FIELDS.has("solaceDisplayName")).toBe(false);
        expect(STEP1_FIELDS.has("taskAuthenticationType")).toBe(false);
    });
});

describe("routeValidationFieldError", () => {
    test("routes step-1 field to formMethods and navigates back to step 1 if needed", () => {
        const formMethods = makeFormMethods();
        const additionalInfo = makeFormMethods();
        const stepper = makeStepper("additional-info");

        const navigatedToStep2 = routeValidationFieldError("agentUrl", "Invalid URL", formMethods, additionalInfo, stepper);

        expect(navigatedToStep2).toBe(false);
        expect(formMethods.setError).toHaveBeenCalledWith("agentUrl", {
            type: "manual",
            message: "Invalid URL",
        });
        expect(additionalInfo.setError).not.toHaveBeenCalled();
        expect(stepper.goTo).toHaveBeenCalledWith("connect");
    });

    test("step-1 field while already on step 1 does NOT navigate", () => {
        const formMethods = makeFormMethods();
        const stepper = makeStepper("connect");

        routeValidationFieldError("agentUrl", "Invalid", formMethods, null, stepper);
        expect(stepper.goTo).not.toHaveBeenCalled();
    });

    test("routes step-2 field to additionalInfoFormMethods and returns true", () => {
        const formMethods = makeFormMethods();
        const additionalInfo = makeFormMethods();
        const stepper = makeStepper("connect");

        const navigatedToStep2 = routeValidationFieldError("solaceDisplayName", "Too short", formMethods, additionalInfo, stepper);

        expect(navigatedToStep2).toBe(true);
        expect(additionalInfo.setError).toHaveBeenCalledWith("solaceDisplayName", {
            type: "manual",
            message: "Too short",
        });
        expect(formMethods.setError).not.toHaveBeenCalled();
    });

    test("null form methods are tolerated", () => {
        const stepper = makeStepper();
        expect(() => routeValidationFieldError("agentUrl", "x", null, null, stepper)).not.toThrow();
    });
});

describe("applyValidationErrorsToForms", () => {
    test("routes a mix of step-1 and step-2 errors and reports the step-2 hit", () => {
        const formMethods = makeFormMethods();
        const additionalInfo = makeFormMethods();
        const stepper = makeStepper("additional-info");

        const shouldNav = applyValidationErrorsToForms(
            {
                agentUrl: ["Invalid URL"],
                solaceDisplayName: ["Too short", "ignored second"],
            },
            formMethods,
            additionalInfo,
            stepper
        );

        expect(shouldNav).toBe(true);
        expect(formMethods.setError).toHaveBeenCalledWith("agentUrl", expect.any(Object));
        expect(additionalInfo.setError).toHaveBeenCalledWith("solaceDisplayName", expect.objectContaining({ message: "Too short" }));
    });

    test("only-step-1 errors return false", () => {
        const formMethods = makeFormMethods();
        const stepper = makeStepper("connect");
        const shouldNav = applyValidationErrorsToForms({ agentUrl: ["Invalid"] }, formMethods, null, stepper);
        expect(shouldNav).toBe(false);
    });

    test("skips empty / non-array entries", () => {
        const formMethods = makeFormMethods();
        const additionalInfo = makeFormMethods();
        const stepper = makeStepper();
        applyValidationErrorsToForms({ agentUrl: [], solaceDisplayName: null as unknown as string[] }, formMethods, additionalInfo, stepper);
        expect(formMethods.setError).not.toHaveBeenCalled();
        expect(additionalInfo.setError).not.toHaveBeenCalled();
    });
});

describe("buildConnectSuccessMessage", () => {
    test.each([
        [false, false, "Remote agent connected successfully"],
        [false, true, "Remote agent connected and deployed successfully"],
        [true, false, "Remote agent updated successfully"],
        [true, true, "Remote agent updated and deployed successfully"],
    ])("isEditMode=%s deployed=%s → %s", (isEditMode, deployed, expected) => {
        expect(buildConnectSuccessMessage(isEditMode, deployed)).toBe(expected);
    });
});

describe("mapBadRequestOrConflict", () => {
    test("duplicate-name detail returns the duplicate-name dialog", () => {
        const additionalInfo = makeFormMethods();
        const result = mapBadRequestOrConflict(new ApiV2Error(409, "a remote agent with this name already exists"), false, { agentCardLocation: "well_known" }, null, additionalInfo, makeStepper());
        expect(result.errorTitle).toBe("Duplicate Agent Name");
        expect(result.shouldNavigateToStep2).toBe(true);
        expect(additionalInfo.setError).toHaveBeenCalledWith("solaceDisplayName", expect.any(Object));
    });

    test("duplicate-name picks agentName for no-card mode", () => {
        const additionalInfo = makeFormMethods();
        mapBadRequestOrConflict(new ApiV2Error(409, "duplicate"), false, { agentCardLocation: "none" }, null, additionalInfo, makeStepper());
        expect(additionalInfo.setError).toHaveBeenCalledWith("agentName", expect.any(Object));
    });

    test("validationDetails map produces a Validation Error and routes fields", () => {
        const formMethods = makeFormMethods();
        const additionalInfo = makeFormMethods();
        const result = mapBadRequestOrConflict(
            new ApiV2Error(422, "Validation failed", { validationDetails: { agentUrl: ["Invalid"], solaceDisplayName: ["Too short"] } }),
            false,
            { agentCardLocation: "well_known" },
            formMethods,
            additionalInfo,
            makeStepper()
        );
        expect(result.errorTitle).toBe("Validation Error");
        expect(result.shouldNavigateToStep2).toBe(true);
    });

    test("falls back to the platform-provided message when no validation details", () => {
        const result = mapBadRequestOrConflict(new ApiV2Error(400, "Bad payload"), false, {}, null, null, makeStepper());
        expect(result.errorTitle).toBe("Connection Failed");
        expect(result.errorMessage).toBe("Bad payload");
    });

    test("isEditMode flips the fallback title to Update Failed", () => {
        const result = mapBadRequestOrConflict(new ApiV2Error(400, ""), true, {}, null, null, makeStepper());
        expect(result.errorTitle).toBe("Update Failed");
    });
});

describe("mapConnectError", () => {
    test.each([
        [501, "Feature Not Enabled", "Remote A2A agents feature is not enabled on the server. Please contact your administrator."],
        [403, "Permission Denied", "You don't have permission to create remote agents."],
    ])("HTTP %i → %s", (status, expectedTitle, expectedMessage) => {
        const result = mapConnectError(new ApiV2Error(status, "refused"), false, {}, null, null, makeStepper());
        expect(result).toEqual({ errorTitle: expectedTitle, errorMessage: expectedMessage, shouldNavigateToStep2: false });
    });

    test("HTTP 403 mentions update/connect based on isEditMode", () => {
        expect(mapConnectError(new ApiV2Error(403, "Forbidden"), true, {}, null, null, makeStepper()).errorMessage).toContain("update");
        expect(mapConnectError(new ApiV2Error(403, "Forbidden"), false, {}, null, null, makeStepper()).errorMessage).toContain("create");
    });

    test.each([400, 422])("HTTP %i sets each field error on its form and sends a step-2 error to step 2", status => {
        const formMethods = makeFormMethods();
        const additionalInfo = makeFormMethods();
        const error = new ApiV2Error(status, "Validation failed: agentUrl: Invalid; solaceDisplayName: Too short", {
            validationDetails: { agentUrl: ["Invalid"], solaceDisplayName: ["Too short"] },
        });

        const result = mapConnectError(error, false, { agentCardLocation: "well_known" }, formMethods, additionalInfo, makeStepper("connect"));

        expect(result).toMatchObject({ errorTitle: "Validation Error", shouldNavigateToStep2: true });
        expect(formMethods.setError).toHaveBeenCalledWith("agentUrl", { type: "manual", message: "Invalid" });
        expect(additionalInfo.setError).toHaveBeenCalledWith("solaceDisplayName", { type: "manual", message: "Too short" });
    });

    test("HTTP 409 on a taken name returns the duplicate-name dialog", () => {
        const additionalInfo = makeFormMethods();
        const result = mapConnectError(new ApiV2Error(409, "a remote agent with this name already exists"), false, { agentCardLocation: "well_known" }, null, additionalInfo, makeStepper());
        expect(result).toMatchObject({ errorTitle: "Duplicate Agent Name", shouldNavigateToStep2: true });
        expect(additionalInfo.setError).toHaveBeenCalledWith("solaceDisplayName", expect.objectContaining({ message: "This name already exists. Please choose a different name." }));
    });

    test("HTTP 500 returns the generic Connection Failed dialog with the platform message", () => {
        const result = mapConnectError(new ApiV2Error(500, "boom"), false, {}, null, null, makeStepper());
        expect(result).toEqual({ errorTitle: "Connection Failed", errorMessage: "boom", shouldNavigateToStep2: false });
    });

    test("a network error returns the generic dialog with its message", () => {
        const result = mapConnectError(new TypeError("Failed to fetch"), false, {}, null, null, makeStepper());
        expect(result).toEqual({ errorTitle: "Connection Failed", errorMessage: "Failed to fetch", shouldNavigateToStep2: false });
    });

    test("isEditMode generic returns Update Failed", () => {
        const result = mapConnectError(new ApiV2Error(500, "boom"), true, {}, null, null, makeStepper());
        expect(result.errorTitle).toBe("Update Failed");
    });
});

describe("runDeployAfterSave", () => {
    const deploySpy = vi.fn();

    beforeEach(() => {
        deploySpy.mockReset();
    });

    test("a successful deploy → failed=false", async () => {
        deploySpy.mockResolvedValue({ id: "d", agentId: "a", status: "success", action: "deploy" });
        const r = await runDeployAfterSave("a-1", deploySpy);
        expect(r.failed).toBe(false);
    });

    test("a failed deploy record with no message → generic message", async () => {
        deploySpy.mockResolvedValue({ id: "d", agentId: "a", status: "failed", action: "deploy" });
        const r = await runDeployAfterSave("a-1", deploySpy);
        expect(r).toEqual({ failed: true, message: "Deployment failed." });
    });

    test("transport error → failed=true with the error message", async () => {
        deploySpy.mockRejectedValue(new Error("network down"));
        const r = await runDeployAfterSave("a-1", deploySpy);
        expect(r.failed).toBe(true);
        expect(r.message).toContain("network down");
    });

    describe("on /api/v2", () => {
        const fetchMock = vi.fn<typeof fetch>();
        const respond = (status: number, body?: unknown, headers?: HeadersInit) => fetchMock.mockResolvedValueOnce(new Response(body === undefined ? null : JSON.stringify(body), { status, headers }));

        beforeEach(() => {
            vi.stubGlobal("fetch", fetchMock);
        });

        afterEach(() => {
            fetchMock.mockReset();
            vi.unstubAllGlobals();
        });

        test("a deploy that ran and failed (502) surfaces the kept record's failure", async () => {
            respond(502, { message: "Unable to deploy agent", errorId: "e1" }, { Location: "/api/v2/platform/agentDeployments/d1" });
            respond(200, {
                data: {
                    id: "d1",
                    agentId: "a-1",
                    status: "failed",
                    action: "deploy",
                    failure: { reason: "awe_timeout", message: "AWE timeout", errorId: "e1" },
                    createdBy: "u1",
                    createdTime: "2026-01-02T03:04:05.678Z",
                    updatedTime: null,
                },
            });

            await expect(runDeployAfterSave("a-1", deployRemoteAgentV2)).resolves.toEqual({ failed: true, message: "AWE timeout" });
        });

        test("a failed deploy whose record the caller cannot read surfaces the deploy's own error", async () => {
            respond(502, { message: "Unable to deploy agent", errorId: "e1" }, { Location: "/api/v2/platform/agentDeployments/d1" });
            respond(403, { message: "Forbidden", errorId: "e2" });

            await expect(runDeployAfterSave("a-1", deployRemoteAgentV2)).resolves.toEqual({ failed: true, message: "Unable to deploy agent" });
        });

        test("a deploy the server refused (422) surfaces its validation message", async () => {
            respond(422, { message: "Validation failed", errorId: "e3", validationDetails: { skipIfCurrent: ["must be left out for undeploy"] } });

            await expect(runDeployAfterSave("a-1", deployRemoteAgentV2)).resolves.toEqual({ failed: true, message: "Validation failed: skipIfCurrent: must be left out for undeploy" });
        });
    });
});

describe("buildWellKnownUrl", () => {
    test("appends the well-known agent-card path", () => {
        expect(buildWellKnownUrl("https://api.example.com")).toBe("https://api.example.com/.well-known/agent-card.json");
    });

    test("trims a single trailing slash so the suffix is not double-slashed", () => {
        expect(buildWellKnownUrl("https://a2a-test.example.net/addition/")).toBe("https://a2a-test.example.net/addition/.well-known/agent-card.json");
    });

    test("trims multiple trailing slashes", () => {
        expect(buildWellKnownUrl("https://api.example.com///")).toBe("https://api.example.com/.well-known/agent-card.json");
    });
});
