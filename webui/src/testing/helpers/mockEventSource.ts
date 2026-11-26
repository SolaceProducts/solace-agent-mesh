// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export class MockEventSource {
    static instances: MockEventSource[] = [];
    url: string;
    readyState = 0;
    onopen: ((event: Event) => void) | null = null;
    onmessage: ((event: MessageEvent) => void) | null = null;
    onerror: ((event: Event) => void) | null = null;
    private readonly listeners = new Map<string, Set<EventListener>>();

    constructor(url: string) {
        this.url = url;
        MockEventSource.instances.push(this);
    }

    static created(endpoint: string): MockEventSource[] {
        return MockEventSource.instances.filter(source => source.url.includes(endpoint));
    }

    addEventListener(type: string, listener: EventListener) {
        const set = this.listeners.get(type) ?? new Set();
        set.add(listener);
        this.listeners.set(type, set);
    }

    removeEventListener(type: string, listener: EventListener) {
        this.listeners.get(type)?.delete(listener);
    }

    close() {
        this.readyState = 2;
    }

    open() {
        this.readyState = 1;
        this.onopen?.(new Event("open"));
    }

    fail() {
        this.onerror?.(new Event("error"));
    }

    emit(type: string, data: string) {
        this.listeners.get(type)?.forEach(listener => listener(new MessageEvent(type, { data })));
    }
}
