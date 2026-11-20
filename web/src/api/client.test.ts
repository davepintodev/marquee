import { expect, test, vi } from "vitest";
import { ApiError, createApi } from "./client";

const ok = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

test("requests and image URLs go under the configured base path", async () => {
  const urls: string[] = [];
  const fetchStub = vi.fn(async (r: Request) => {
    urls.push(r.url);
    return ok({ items: [] });
  });
  const api = createApi({ baseUrl: "http://host.test/marquee/", fetch: fetchStub as unknown as typeof fetch });

  await api.wall();

  expect(urls).toEqual(["http://host.test/marquee/api/wall"]);
  expect(api.imageUrl("/api/images/poster/movie-1")).toBe("http://host.test/marquee/api/images/poster/movie-1");
});

test("same-origin by default: image URLs stay relative", () => {
  expect(createApi({}).imageUrl("/api/images/poster/movie-1")).toBe("/api/images/poster/movie-1");
});

test("search sends the query URL-encoded", async () => {
  const urls: string[] = [];
  const fetchStub = vi.fn(async (r: Request) => {
    urls.push(r.url);
    return ok({ results: [] });
  });
  const api = createApi({ baseUrl: "http://host.test", fetch: fetchStub as unknown as typeof fetch });

  await api.search("dune & más");

  expect(new URL(urls[0]).searchParams.get("q")).toBe("dune & más");
});

test("an error body's message becomes the ApiError message, whatever the status", async () => {
  const fetchStub = vi.fn(async () => new Response(JSON.stringify({ error: "TMDB did not answer" }), { status: 502, headers: { "Content-Type": "application/json" } }));
  const api = createApi({ baseUrl: "http://host.test", fetch: fetchStub as unknown as typeof fetch });

  await expect(api.search("x")).rejects.toThrow(new ApiError("TMDB did not answer", 502));
});

test("a 200 with a body that is not the contract's shape is an ApiError, not a crash later", async () => {
  const fetchStub = vi.fn(async () => ok({ wrong: true }));
  const api = createApi({ baseUrl: "http://host.test", fetch: fetchStub as unknown as typeof fetch });

  await expect(api.wall()).rejects.toBeInstanceOf(ApiError);
});

test("an aborted request rethrows the abort so callers can ignore it", async () => {
  const ctl = new AbortController();
  const fetchStub = vi.fn(async (r: Request) => {
    await new Promise((_, rej) => {
      // Like fetch, which rejects at once for a signal that is already aborted.
      if (r.signal.aborted) rej(r.signal.reason);
      r.signal.addEventListener("abort", () => rej(r.signal.reason));
    });
    return ok({});
  });
  const api = createApi({ baseUrl: "http://host.test", fetch: fetchStub as unknown as typeof fetch });

  const p = api.wall(ctl.signal);
  ctl.abort();

  await expect(p).rejects.toMatchObject({ name: "AbortError" });
});

test("removing a title accepts the 204 with no body and turns an error body into its message", async () => {
  const answers = [new Response(null, { status: 204 }), new Response(JSON.stringify({ error: "that title is not in the library" }), { status: 404, headers: { "Content-Type": "application/json" } })];
  const fetchStub = vi.fn(async () => answers.shift()!);
  const api = createApi({ baseUrl: "http://host.test", fetch: fetchStub as unknown as typeof fetch });

  await expect(api.removeTitle("series-1")).resolves.toBeUndefined();
  await expect(api.removeTitle("series-1")).rejects.toThrow("that title is not in the library");
});

class Stream {
  closed = false;
  private fns = new Map<string, (e: Event) => void>();
  addEventListener(name: string, fn: (e: Event) => void) {
    this.fns.set(name, fn);
  }
  close() {
    this.closed = true;
  }
  send(name: string, data = "") {
    this.fns.get(name)?.(new MessageEvent(name, { data }));
  }
}

test("events delivers progress and title messages, skips garbage and says when the stream came back", () => {
  const stream = new Stream();
  const urls: string[] = [];
  const api = createApi({ baseUrl: "http://host.test/", eventSource: (u) => (urls.push(u), stream as unknown as EventSource) });
  const got: unknown[] = [];

  const stop = api.events((e) => got.push(e));
  stream.send("open");
  stream.send("progress", JSON.stringify({ downloadId: "d", percent: 5 }));
  stream.send("progress", "not json");
  stream.send("title", JSON.stringify({ titleId: "movie-1", badge: null }));
  stream.send("open");
  stop();

  expect(urls).toEqual(["http://host.test/api/events"]);
  expect(got).toEqual([
    { type: "progress", data: { downloadId: "d", percent: 5 } },
    { type: "title", data: { titleId: "movie-1", badge: null } },
    { type: "reconnected" },
  ]);
  expect(stream.closed).toBe(true);
});

test("every listener shares one stream, which closes with the last of them", () => {
  const streams: Stream[] = [];
  const api = createApi({ baseUrl: "http://host.test", eventSource: () => (streams.push(new Stream()), streams.at(-1) as unknown as EventSource) });
  const a: unknown[] = [];
  const b: unknown[] = [];

  const stopA = api.events((e) => a.push(e));
  const stopB = api.events((e) => b.push(e));
  expect(streams).toHaveLength(1);
  streams[0].send("title", JSON.stringify({ titleId: "movie-1", badge: null }));
  expect(a).toHaveLength(1);
  expect(b).toHaveLength(1);

  stopA();
  stopA();
  expect(streams[0].closed).toBe(false);
  streams[0].send("title", JSON.stringify({ titleId: "movie-2", badge: null }));
  expect(a).toHaveLength(1);
  expect(b).toHaveLength(2);

  stopB();
  expect(streams[0].closed).toBe(true);
  api.events(() => {})();
  expect(streams).toHaveLength(2);
});

test("events does nothing where there is no EventSource", () => {
  const api = createApi({});
  expect(() => api.events(() => {})()).not.toThrow();
});

test("every request carries the token the caller holds, and none when there is none", async () => {
  const seen: (string | null)[] = [];
  const fetchStub = vi.fn(async (r: Request) => {
    seen.push(r.headers.get("Authorization"));
    return ok({ items: [] });
  });
  let token: string | null = null;
  const api = createApi({ baseUrl: "http://host.test", fetch: fetchStub as unknown as typeof fetch, getToken: () => token });

  await api.wall();
  token = "t0ken";
  await api.wall();

  expect(seen).toEqual([null, "Bearer t0ken"]);
});

test("a 401 tells the caller the token is needed and still fails the call", async () => {
  const fetchStub = vi.fn(async () => new Response(JSON.stringify({ error: "this marquee needs its API token" }), { status: 401, headers: { "Content-Type": "application/json" } }));
  const onUnauthorized = vi.fn();
  const api = createApi({ baseUrl: "http://host.test", fetch: fetchStub as unknown as typeof fetch, onUnauthorized });

  await expect(api.wall()).rejects.toMatchObject({ status: 401 });
  expect(onUnauthorized).toHaveBeenCalledTimes(1);
});

test("a caller is told when a request that carried the token was accepted, and not for one that carried none", async () => {
  const fetchStub = vi.fn(async () => ok({ items: [] }));
  const onAuthorized = vi.fn();
  let token: string | null = null;
  const api = createApi({ baseUrl: "http://host.test", fetch: fetchStub as unknown as typeof fetch, getToken: () => token, onAuthorized });

  await api.wall();
  expect(onAuthorized).not.toHaveBeenCalled();

  token = "t0ken";
  await api.wall();
  expect(onAuthorized).toHaveBeenCalledTimes(1);
});

test("a late acceptance of an older token does not count as the held token being accepted", async () => {
  const onAuthorized = vi.fn();
  let token: string | null = "old";
  const fetchStub = vi.fn(async () => {
    token = "new";
    return ok({ items: [] });
  });
  const api = createApi({ baseUrl: "http://host.test", fetch: fetchStub as unknown as typeof fetch, getToken: () => token, onAuthorized });

  await api.wall();

  expect(onAuthorized).not.toHaveBeenCalled();
});

test("health reads the contract's shape", async () => {
  const health = { status: "ok", dryRun: false, checks: [], libraryProblems: [] };
  const fetchStub = vi.fn(async (r: Request) => (new URL(r.url).pathname === "/api/health" ? ok(health) : ok({})));
  const api = createApi({ baseUrl: "http://host.test", fetch: fetchStub as unknown as typeof fetch });

  await expect(api.health()).resolves.toEqual(health);
});

// The browser holds the session cookie only once marquee has answered a request that carried the token; a stream opened sooner is refused and never retries.
test("while a token is held, the stream opens only after a request with it has been answered", async () => {
  const streams: Stream[] = [];
  const answer = vi.fn(async () => ok({ items: [] }));
  const api = createApi({
    baseUrl: "http://host.test",
    fetch: answer as unknown as typeof fetch,
    getToken: () => "t0ken",
    eventSource: () => (streams.push(new Stream()), streams.at(-1) as unknown as EventSource),
  });
  const got: unknown[] = [];

  const stop = api.events((e) => got.push(e));
  expect(streams).toHaveLength(0);

  await api.wall();
  expect(streams).toHaveLength(1);
  streams[0].send("title", JSON.stringify({ titleId: "movie-1", badge: null }));
  expect(got).toHaveLength(1);

  await api.wall();
  expect(streams).toHaveLength(1);
  stop();
  expect(streams[0].closed).toBe(true);
});

test("a stream nobody listens to any more is not opened when the answer comes", async () => {
  const streams: Stream[] = [];
  const api = createApi({
    baseUrl: "http://host.test",
    fetch: (async () => ok({ items: [] })) as unknown as typeof fetch,
    getToken: () => "t0ken",
    eventSource: () => (streams.push(new Stream()), streams.at(-1) as unknown as EventSource),
  });

  api.events(() => {})();
  await api.wall();
  expect(streams).toHaveLength(0);
});

test("a refused token makes the next stream wait for a request that is answered", async () => {
  const streams: Stream[] = [];
  let status = 200;
  const api = createApi({
    baseUrl: "http://host.test",
    fetch: (async () => (status === 200 ? ok({ items: [] }) : new Response(JSON.stringify({ error: "no" }), { status, headers: { "Content-Type": "application/json" } }))) as unknown as typeof fetch,
    getToken: () => "t0ken",
    eventSource: () => (streams.push(new Stream()), streams.at(-1) as unknown as EventSource),
  });

  await api.wall();
  status = 401;
  await expect(api.wall()).rejects.toMatchObject({ status: 401 });
  api.events(() => {});
  expect(streams).toHaveLength(0);

  status = 200;
  await api.wall();
  expect(streams).toHaveLength(1);
});

test("a refused token closes the stream it opened, and the next answer opens a new one", async () => {
  const streams: Stream[] = [];
  let status = 200;
  const api = createApi({
    baseUrl: "http://host.test",
    fetch: (async () => (status === 200 ? ok({ items: [] }) : new Response(JSON.stringify({ error: "no" }), { status, headers: { "Content-Type": "application/json" } }))) as unknown as typeof fetch,
    getToken: () => "t0ken",
    eventSource: () => (streams.push(new Stream()), streams.at(-1) as unknown as EventSource),
  });
  api.events(() => {});

  await api.wall();
  status = 401;
  await expect(api.wall()).rejects.toMatchObject({ status: 401 });
  expect(streams[0].closed).toBe(true);

  status = 200;
  await api.wall();
  expect(streams).toHaveLength(2);
});

test("a late refusal of a token that is no longer held leaves the stream alone", async () => {
  const streams: Stream[] = [];
  let token = "old";
  let refuseOld: () => void = () => {};
  const api = createApi({
    baseUrl: "http://host.test",
    fetch: (async (r: Request) =>
      r.headers.get("Authorization") === "Bearer old"
        ? new Promise<Response>((resolve) => {
            refuseOld = () => resolve(new Response(JSON.stringify({ error: "no" }), { status: 401, headers: { "Content-Type": "application/json" } }));
          })
        : ok({ items: [] })) as unknown as typeof fetch,
    getToken: () => token,
    eventSource: () => (streams.push(new Stream()), streams.at(-1) as unknown as EventSource),
  });

  const late = api.wall().catch(() => {});
  token = "new";
  await api.wall();
  refuseOld();
  await late;

  api.events(() => {});
  expect(streams).toHaveLength(1);
});

test("with no token held the stream opens at once", () => {
  const streams: Stream[] = [];
  const api = createApi({ baseUrl: "http://host.test", eventSource: () => (streams.push(new Stream()), streams.at(-1) as unknown as EventSource) });
  api.events(() => {});
  expect(streams).toHaveLength(1);
});
