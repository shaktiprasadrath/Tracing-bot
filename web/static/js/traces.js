import { api } from "./api.js";

// renderTraces implements the Traces screen's search slice (F12 §4.4 /
// FR-F12-3): filter bar (service, errors-only) + results table, against the
// real GET /v1/traces endpoint.
export async function renderTraces(root) {
  root.innerHTML = `
    <h2>Traces</h2>
    <div class="filters">
      <input id="f-service" placeholder="service" />
      <label><input type="checkbox" id="f-errors" /> errors only</label>
      <button id="f-search">Search</button>
    </div>
    <div id="results"><p class="loading">Loading…</p></div>
  `;

  const run = () => search(root);
  root.querySelector("#f-search").addEventListener("click", run);
  run();
}

async function search(root) {
  const results = root.querySelector("#results");
  const service = root.querySelector("#f-service").value.trim();
  const errorsOnly = root.querySelector("#f-errors").checked;
  results.innerHTML = `<p class="loading">Searching…</p>`;

  let page;
  try {
    page = await api.traces({ service, errors_only: errorsOnly ? "true" : "" });
  } catch (err) {
    results.innerHTML = `<p class="empty">Search failed: ${escapeHTML(err.message)}</p>`;
    return;
  }

  const traces = page.Traces || [];
  if (traces.length === 0) {
    results.innerHTML = `<p class="empty">No traces matched.</p>`;
    return;
  }

  results.innerHTML = `
    <table>
      <thead><tr><th>Root service</th><th>Duration</th><th>Errors</th><th>Keep reason</th></tr></thead>
      <tbody>
        ${traces.map((t) => `
          <tr>
            <td>${escapeHTML(t.RootService || "")}</td>
            <td>${Math.round((t.DurationNanos || 0) / 1e6)} ms</td>
            <td>${t.ErrorCount > 0 ? `<span class="badge error">${t.ErrorCount}</span>` : `<span class="badge ok">0</span>`}</td>
            <td>${escapeHTML(String(t.KeepReason ?? ""))}</td>
          </tr>`).join("")}
      </tbody>
    </table>
  `;
}

function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[c]));
}
