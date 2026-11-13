// API-first: everything the UI does is an API call (Master Spec section 1).

const TENANT_KEY = "ce_tenant_id";

export function getTenantId(): string {
  return localStorage.getItem(TENANT_KEY) ?? "";
}

export function setTenantId(id: string): void {
  localStorage.setItem(TENANT_KEY, id);
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const resp = await fetch(path, {
    method,
    headers: {
      "Content-Type": "application/json",
      "X-Tenant-ID": getTenantId(),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!resp.ok) {
    const text = await resp.text();
    throw new Error(`${resp.status}: ${text.slice(0, 300)}`);
  }
  if (resp.status === 204) return undefined as T;
  return (await resp.json()) as T;
}

export const api = {
  get: <T>(path: string) => call<T>("GET", path),
  post: <T>(path: string, body?: unknown) => call<T>("POST", path, body ?? {}),
  put: <T>(path: string, body: unknown) => call<T>("PUT", path, body),
  del: (path: string) => call<void>("DELETE", path),
};

export function dispatchStream(tenantId: string): EventSource {
  // SSE hub — dispatch board live feed (events: assignment, trip_status, geostamp).
  const url = `/v1/dispatch/stream`;
  const es = new EventSource(url);
  void tenantId; // EventSource cannot set headers; tenant resolution moves to
  // a signed query token at the APISIX edge in production.
  return es;
}
