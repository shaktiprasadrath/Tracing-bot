import { renderOverview } from "./overview.js";
import { renderTraces } from "./traces.js";

// app.js is the SPA's whole client-side router (History API, FR-F12-2):
// no framework, no build step. Two routes are wired this wave (Overview,
// Traces) — see web/web.go's doc comment for what is deferred.
const routes = {
  "/": renderOverview,
  "/traces": renderTraces,
};

const view = document.getElementById("view");
const nav = document.getElementById("nav");

function currentRoute() {
  return routes[location.pathname] ? location.pathname : "/";
}

function setActiveNav(path) {
  nav.querySelectorAll("a").forEach((a) => {
    a.classList.toggle("active", a.dataset.route === path);
  });
}

async function render() {
  const path = currentRoute();
  setActiveNav(path);
  await routes[path](view);
}

nav.addEventListener("click", (e) => {
  const a = e.target.closest("a[data-route]");
  if (!a) return;
  e.preventDefault();
  history.pushState({}, "", a.getAttribute("href"));
  render();
});

window.addEventListener("popstate", render);

render();
