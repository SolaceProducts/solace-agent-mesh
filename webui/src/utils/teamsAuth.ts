// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface TeamsSessionTokens {
    accessToken: string;
    samAccessToken?: string | null;
    refreshToken?: string | null;
}

const HANDSHAKE_TIMEOUT_MS = 2_000;

let teamsContext: Promise<boolean> | null = null;
let answeredInTeams = false;

async function loadTeams() {
    return import("@microsoft/teams-js");
}

const TIMED_OUT = Symbol("timed out");

async function askHostWithin(ms: number): Promise<boolean> {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const deadline = new Promise<typeof TIMED_OUT>(resolve => {
        timer = setTimeout(() => resolve(TIMED_OUT), ms);
    });

    try {
        const loaded = await Promise.race([loadTeams(), deadline]);
        if (loaded === TIMED_OUT) throw new Error("the Teams SDK did not load in time");

        const handshake = loaded.app.initialize().then(() => {
            answeredInTeams = true;
            return true;
        });
        handshake.catch(() => {});

        const answer = await Promise.race([handshake.catch(() => false), deadline]);
        return answer !== TIMED_OUT && answer;
    } finally {
        if (timer) clearTimeout(timer);
    }
}

function forgetUnlearnedHostAndAssumeOutsideTeams(): boolean {
    teamsContext = null;
    return false;
}

export function isInTeams(): Promise<boolean> {
    if (answeredInTeams) return Promise.resolve(true);
    teamsContext ??= askHostWithin(HANDSHAKE_TIMEOUT_MS).catch(forgetUnlearnedHostAndAssumeOutsideTeams);
    return teamsContext;
}

export function startTeamsHandshakeIfPossible(): void {
    if (couldBeTeamsTab()) void isInTeams();
}

export function knownToBeInTeams(): boolean {
    return answeredInTeams;
}

export function couldBeTeamsTab(): boolean {
    return globalThis.top !== globalThis.self || "nativeInterface" in globalThis;
}

export function couldBeTeamsAuthWindow(): boolean {
    return Boolean(globalThis.opener) || couldBeTeamsTab();
}

export async function signInThroughTeams(loginUrl: string): Promise<TeamsSessionTokens | null> {
    if (!(await isInTeams())) return null;

    try {
        const { authentication } = await loadTeams();
        const result = await authentication.authenticate({
            url: new URL(loginUrl, globalThis.location.href).toString(),
            width: 600,
            height: 700,
        });
        return parseTokens(result);
    } catch (error) {
        console.warn("[auth] the Teams sign-in window closed without returning a session.", error);
        return null;
    }
}

export async function completeTeamsSignIn(tokens: TeamsSessionTokens): Promise<boolean> {
    if (!(await isInTeams())) return false;

    try {
        const { authentication } = await loadTeams();
        authentication.notifySuccess(JSON.stringify(tokens));
        return true;
    } catch (error) {
        console.warn("[auth] could not hand the session back through Teams; completing in this window.", error);
        return false;
    }
}

export async function failTeamsSignIn(reason: string): Promise<void> {
    if (!(await isInTeams())) return;
    try {
        const { authentication } = await loadTeams();
        authentication.notifyFailure(reason);
    } catch {}
}

function parseTokens(result: unknown): TeamsSessionTokens | null {
    if (typeof result !== "string" || !result) return null;
    try {
        const parsed = JSON.parse(result) as Partial<TeamsSessionTokens>;
        if (typeof parsed.accessToken !== "string" || !parsed.accessToken) return null;
        return {
            accessToken: parsed.accessToken,
            samAccessToken: typeof parsed.samAccessToken === "string" ? parsed.samAccessToken : null,
            refreshToken: typeof parsed.refreshToken === "string" ? parsed.refreshToken : null,
        };
    } catch {
        return null;
    }
}
