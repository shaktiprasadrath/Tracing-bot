import { api } from "./api.js";

// renderOverview implements the Overview screen's minimum viable slice
// (F12 §4.4: RED tiles, active-incident cards) against the real
// GET /v1/overview endpoint — no mock data.
export async function renderOverview(root) {
  root.innerHTML = `<p class="loading">Loading overview…</p>`;
  let data;
  try {
    data = await api.overview();
  } catch (err) {
    root.innerHTML = `<p class="empty">Could not load overview: ${escapeHTML(err.message)}</p>`;
    return;
  }

  const incidents = data.incidents || [];
  const tiles = data.red_tiles || [];

  const tilesHTML = tiles.length
    ? tiles.map((t) => `
        <div class="tile">
          <h3>${escapeHTML(t.service)}</h3>
          <div class="value">${(t.samples || []).length} samples</div>
        </div>`).join("")
    : `<p class="empty">No RED data for the selected window.</p>`;

  const incidentsHTML = incidents.length
    ? `<table>
        <thead><tr><th>Title</th><th>Status</th><th>Severity</th><th>Score</th></tr></thead>
        <tbody>
          ${incidents.map((i) => `
            <tr>
              <td>${escapeHTML(i.Title || "")}</td>
              <td>${escapeHTML(String(i.Status ?? ""))}</td>
              <td>${escapeHTML(String(i.Severity ?? ""))}</td>
              <td>${escapeHTML(String(i.Score ?? ""))}</td>
            </tr>`).join("")}
        </tbody>
      </table>`
    : `<p class="empty">No active incidents.</p>`;

  root.innerHTML = `
    <h2>Overview</h2>
    <div class="tiles">${tilesHTML}</div>
    <h3>Active incidents</h3>
    ${incidentsHTML}
  `;
}

function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[c]));
}
