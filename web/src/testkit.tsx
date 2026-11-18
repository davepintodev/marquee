import { render } from "@testing-library/react";
import { vi } from "vitest";
import { App } from "./App";
import { createApi } from "./api/client";

// jsdom's Request rejects relative URLs, so tests use an absolute base.
export const BASE = "http://marquee.test";

export type Handler = (url: URL, init: Request) => Response | Promise<Response>;

export function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

// A stand-in for the browser's EventSource that tests push messages into.
export class FakeEventSource {
  static all: FakeEventSource[] = [];
  readonly url: string;
  closed = false;
  private listeners = new Map<string, ((e: MessageEvent) => void)[]>();
  constructor(url: string) {
    this.url = url;
    FakeEventSource.all.push(this);
  }
  addEventListener(name: string, fn: (e: MessageEvent) => void) {
    this.listeners.set(name, [...(this.listeners.get(name) ?? []), fn]);
  }
  close() {
    this.closed = true;
  }
  emit(name: string, data: unknown) {
    for (const fn of this.listeners.get(name) ?? []) fn(new MessageEvent(name, { data: typeof data === "string" ? data : JSON.stringify(data) }));
  }
  open() {
    this.emit("open", "");
  }
  static open() {
    return FakeEventSource.all.filter((s) => !s.closed);
  }
}

export function setupApp(handler: Handler, hash: string) {
  FakeEventSource.all = [];
  window.location.hash = hash;
  const calls: URL[] = [];
  const writes: { method: string; path: string; body: unknown }[] = [];
  const fetchStub = vi.fn(async (input: Request) => {
    const url = new URL(input.url);
    calls.push(url);
    if (input.method !== "GET") {
      const text = await input.clone().text();
      writes.push({ method: input.method, path: url.pathname, body: text ? JSON.parse(text) : undefined });
    }
    return handler(url, input);
  });
  const api = createApi({
    baseUrl: BASE,
    fetch: fetchStub as unknown as typeof fetch,
    eventSource: (url) => new FakeEventSource(url) as unknown as EventSource,
  });
  render(<App api={api} />);
  return { calls, writes };
}
