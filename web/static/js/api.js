// api.js — the SPA's single fetch wrapper. Every screen module calls
// through here so there is exactly one place that knows the /v1 prefix,
// error envelope shape ({"error":{"code","message"}}, see
// internal/api/httpjson.go) and credentials mode.
const BASE = "/v1";

async function request(path, opts = {}) {
  const res = await fetch(BASE + path, {
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", ...(opts.headers || {}) },
    ...opts,
  });
  const isJSON = (res.headers.get("content-type") || "").includes("application/json");
  const body = isJSON ? await res.json().catch(() => null) : null;
  if (!res.ok) {
    const msg = body && body.error ? body.error.message : res.statusText;
    throw new Error(`${res.status} ${msg}`);
  }
  return body;
}

export const api = {
  overview: (params = {}) => request("/overview" + qs(params)),
  incidents: () => request("/incidents"),
  traces: (params = {}) => request("/traces" + qs(params)),
  trace: (id) => request(`/traces/${id}`),
  topology: (params = {}) => request("/topology" + qs(params)),
};

function qs(params) {
  const entries = Object.entries(params).filter(([, v]) => v !== undefined && v !== "");
  if (entries.length === 0) return "";
  return "?" + new URLSearchParams(entries).toString();
}
