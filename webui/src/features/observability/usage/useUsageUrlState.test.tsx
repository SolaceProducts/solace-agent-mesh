// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter, useLocation, useNavigationType } from "react-router-dom";
import { describe, expect, test } from "vitest";

import type { UsageKind } from "@/api/usage";

import { USAGE_KINDS_WITHOUT_USERS } from "./usageKinds";
import { useUsageUrlState } from "./useUsageUrlState";

const TARGET = { kind: "agents" as const, ref: { id: "01a10c9e-1111-7e77-be6a-3cdc8d0b9101" } };
const OTHER = { kind: "agents" as const, ref: { id: "01a10c9e-1111-7e77-be6a-3cdc8d0b9102" } };

function render(initialEntries: string[]) {
    const wrapper = ({ children }: { children: ReactNode }) => <MemoryRouter initialEntries={initialEntries}>{children}</MemoryRouter>;
    return renderHook(() => ({ state: useUsageUrlState(), location: useLocation(), navigationType: useNavigationType() }), { wrapper });
}

describe("useUsageUrlState panel history", () => {
    test("opening pushes the panel; closing steps back over it, so Back does not reopen it", () => {
        const { result } = render(["/usage?type=agents"]);
        act(() => result.current.state.setDetail(TARGET));
        expect(result.current.navigationType).toBe("PUSH");
        expect(result.current.state.serializedDetail).toBe(`agents:id:${TARGET.ref.id}`);
        act(() => result.current.state.setDetail(null));
        expect(result.current.navigationType).toBe("POP");
        expect(result.current.location.search).toBe("?type=agents");
    });

    test("a panel reached by a link closes by replacing the URL, since there is no entry of ours to step back to", () => {
        const { result } = render([`/usage?detail=agents:id:${TARGET.ref.id}`]);
        act(() => result.current.state.setDetail(null));
        expect(result.current.navigationType).toBe("REPLACE");
        expect(result.current.state.serializedDetail).toBeNull();
    });

    test("a range replaced while a panel is open keeps the panel's entry, so closing still steps back", () => {
        const { result } = render(["/usage"]);
        act(() => result.current.state.setDetail(TARGET));
        act(() => result.current.state.setRange({ kind: "preset", preset: "last_7_days" }, { replace: true }));
        expect(result.current.navigationType).toBe("REPLACE");
        act(() => result.current.state.setDetail(null));
        expect(result.current.navigationType).toBe("POP");
        expect(result.current.state.serializedDetail).toBeNull();
    });

    test("opening another panel over an open one swaps it in place, so one Close still closes", () => {
        const { result } = render(["/usage?type=agents"]);
        act(() => result.current.state.setDetail(TARGET));
        act(() => result.current.state.setDetail(OTHER));
        expect(result.current.navigationType).toBe("REPLACE");
        expect(result.current.state.serializedDetail).toBe(`agents:id:${OTHER.ref.id}`);
        act(() => result.current.state.setDetail(null));
        expect(result.current.location.search).toBe("?type=agents");
    });
});

describe("useUsageUrlState tab", () => {
    function renderKinds(initialEntries: string[], kinds?: readonly UsageKind[]) {
        const wrapper = ({ children }: { children: ReactNode }) => <MemoryRouter initialEntries={initialEntries}>{children}</MemoryRouter>;
        return renderHook(() => useUsageUrlState(kinds), { wrapper });
    }

    test("a link to a tab that is not offered opens on the default tab", () => {
        const { result } = renderKinds(["/usage?type=users"], USAGE_KINDS_WITHOUT_USERS);
        expect(result.current.kind).toBe("agents");
    });
});

describe("useUsageUrlState name filter", () => {
    test("a link opens filtered; typing replaces the entry rather than pushing one; clearing or switching tab drops it", () => {
        const { result } = render(["/usage?type=users&name=Olive"]);
        expect(result.current.state.kind).toBe("users");
        expect(result.current.state.nameFilter).toBe("Olive");

        act(() => result.current.state.setNameFilter("Oliver"));
        expect(result.current.navigationType).toBe("REPLACE");
        expect(result.current.location.search).toBe("?type=users&name=Oliver");

        act(() => result.current.state.setNameFilter(""));
        expect(result.current.location.search).toBe("?type=users");

        act(() => result.current.state.setNameFilter("Casey"));
        act(() => result.current.state.setKind("agents"));
        expect(result.current.state.nameFilter).toBe("");
        expect(result.current.location.search).toBe("");
    });
});
