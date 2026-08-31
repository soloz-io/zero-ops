import { describe, expect, it, vi } from "vitest";
import { createAuthClient, hasUserContext } from "../../src/client/index.js";
import type { UserIdentity } from "../../src/client/index.js";

const identity: UserIdentity = {
  userId: "local-uuid",
  subject: "kratos-subject",
  email: "person@example.com",
  role: null,
  tenantId: "acme",
};

const respond = (status: number, body?: unknown) =>
  vi.fn(async () => ({
    status,
    ok: status >= 200 && status < 300,
    json: async () => body,
  })) as unknown as typeof globalThis.fetch;

describe("createAuthClient", () => {
  it("starts unknown, which is not the same as signed out", () => {
    const client = createAuthClient({ fetch: respond(200, { user: identity }) });
    // Rendering a signed-out view in this window is a flash of the wrong screen.
    expect(client.getState().status).toBe("unknown");
  });

  it("reports authenticated with the identity", async () => {
    const client = createAuthClient({ fetch: respond(200, { user: identity }) });
    const state = await client.refresh();
    expect(state.status).toBe("authenticated");
    expect(state.user?.userId).toBe("local-uuid");
  });

  it("treats 401 as signed out", async () => {
    const client = createAuthClient({ fetch: respond(401) });
    expect((await client.refresh()).status).toBe("unauthenticated");
  });

  it("does NOT treat a server failure as signed out", async () => {
    // Signing a person out because a gateway returned 502 is the mistake this
    // distinction exists to prevent.
    const client = createAuthClient({ fetch: respond(502) });
    const state = await client.refresh();
    expect(state.status).toBe("error");
    expect(state.user).toBeNull();
  });

  it("does NOT treat a network failure as signed out", async () => {
    const failing = vi.fn(async () => {
      throw new Error("network down");
    }) as unknown as typeof globalThis.fetch;
    expect((await createAuthClient({ fetch: failing }).refresh()).status).toBe(
      "error",
    );
  });

  it("rejects a success response that carries no subject", async () => {
    const client = createAuthClient({ fetch: respond(200, { user: {} }) });
    expect((await client.refresh()).status).toBe("error");
  });

  it("shares one in-flight request across overlapping refreshes", async () => {
    const fetchImpl = respond(200, { user: identity });
    const client = createAuthClient({ fetch: fetchImpl });
    await Promise.all([client.refresh(), client.refresh(), client.refresh()]);
    // Three components checking on mount must not produce three requests whose
    // responses can resolve out of order.
    expect(fetchImpl).toHaveBeenCalledTimes(1);
  });

  it("notifies subscribers and stops after unsubscribe", async () => {
    const client = createAuthClient({ fetch: respond(200, { user: identity }) });
    const seen: string[] = [];
    const off = client.subscribe((s) => seen.push(s.status));
    await client.refresh();
    off();
    await client.refresh();
    expect(seen).toEqual(["authenticated"]);
  });

  it("supplies auth headers per request for platforms without cookies", async () => {
    const fetchImpl = respond(200, { user: identity });
    const client = createAuthClient({
      fetch: fetchImpl,
      authHeaders: () => ({ authorization: "Bearer native-token" }),
    });
    await client.refresh();
    const init = (fetchImpl as unknown as ReturnType<typeof vi.fn>).mock
      .calls[0]![1];
    expect(init.headers).toEqual({ authorization: "Bearer native-token" });
  });
});

describe("hasUserContext", () => {
  it("is false while authenticated without a tenant-local user", () => {
    // A valid session whose ownership is unknown: anything keyed on userId must
    // wait rather than send null.
    expect(
      hasUserContext({
        status: "authenticated",
        user: { ...identity, userId: null },
        error: null,
      }),
    ).toBe(false);
  });

  it("is true once the tenant-local user is known", () => {
    expect(
      hasUserContext({ status: "authenticated", user: identity, error: null }),
    ).toBe(true);
  });
});
