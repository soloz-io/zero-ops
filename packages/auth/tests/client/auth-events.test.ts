import { describe, expect, it, vi } from "vitest";
import { createAuthClient, type AuthEvent } from "../../src/client/index.js";

const USER = {
  userId: "u_1",
  subject: "sub_1",
  email: "a@b.c",
  role: "member",
  tenantId: "waypoint",
};

function res(init: { status?: number; type?: string; body?: unknown }): Response {
  return {
    status: init.status ?? 200,
    ok: (init.status ?? 200) >= 200 && (init.status ?? 200) < 300,
    type: init.type ?? "basic",
    json: async () => init.body ?? {},
  } as unknown as Response;
}

function clientWith(responses: Response[]) {
  const fetch = vi.fn();
  for (const r of responses) fetch.mockResolvedValueOnce(r);
  const events: AuthEvent[] = [];
  const client = createAuthClient({ fetch: fetch as unknown as typeof globalThis.fetch });
  client.listen((e) => events.push(e));
  return { client, events, fetch };
}

describe("auth events", () => {
  it("emits signedIn when a session is established", async () => {
    const { client, events } = clientWith([res({ body: { user: USER } })]);
    await client.refresh();
    expect(events).toEqual([{ event: "signedIn", data: USER }]);
  });

  it("does NOT emit sessionExpired for a visitor who was never signed in", async () => {
    // The transition unknown -> unauthenticated is an ordinary first load. An
    // application that logs out here would bounce every anonymous visitor.
    const { client, events } = clientWith([res({ status: 401 })]);
    await client.refresh();
    expect(events.map((e) => e.event)).not.toContain("sessionExpired");
  });

  it("emits sessionExpired when an established session becomes invalid", async () => {
    const { client, events } = clientWith([
      res({ body: { user: USER } }),
      res({ status: 401 }),
    ]);
    await client.refresh();
    await client.refresh();
    expect(events.map((e) => e.event)).toEqual(["signedIn", "sessionExpired"]);
  });

  it("treats the gateway's redirect to the identity provider as expiry", async () => {
    // The gateway answers an unauthenticated XHR with a 302 to the provider.
    // With redirect: "manual" that arrives as an opaque response, which is the
    // only same-origin way to learn the session ended.
    const { client, events } = clientWith([
      res({ body: { user: USER } }),
      res({ status: 0, type: "opaqueredirect" }),
    ]);
    await client.refresh();
    await client.refresh();
    expect(events.map((e) => e.event)).toEqual(["signedIn", "sessionExpired"]);
  });

  it("does not report expiry when the check merely failed", async () => {
    // A 502 means the client does not know. Signing someone out because a
    // gateway blipped is the failure this distinction exists to prevent.
    const { client, events } = clientWith([
      res({ body: { user: USER } }),
      res({ status: 502 }),
    ]);
    await client.refresh();
    await client.refresh();
    expect(events.map((e) => e.event)).toEqual(["signedIn", "checkFailed"]);
  });

  it("emits signedOut, not sessionExpired, when the person asks to leave", async () => {
    const { client, events } = clientWith([res({ body: { user: USER } })]);
    await client.refresh();
    client.signOut();
    expect(events.map((e) => e.event)).toEqual(["signedIn", "signedOut"]);
  });

  it("keeps notifying listeners when one throws", async () => {
    const { client } = clientWith([res({ body: { user: USER } })]);
    const seen: string[] = [];
    client.listen(() => {
      throw new Error("faulty handler");
    });
    client.listen((e) => seen.push(e.event));
    vi.spyOn(console, "error").mockImplementation(() => {});
    await client.refresh();
    // The second listener is the one revoking access; it must still run.
    expect(seen).toEqual(["signedIn"]);
  });

  it("unsubscribes", async () => {
    const { client, events } = clientWith([res({ body: { user: USER } })]);
    const stop = client.listen(() => events.push({ event: "signedOut" }));
    stop();
    await client.refresh();
    expect(events.filter((e) => e.event === "signedOut")).toHaveLength(0);
  });

});
