"use strict";

// ---------------------------------------------------------------------------
// Element references
// ---------------------------------------------------------------------------
const summary = document.querySelector("#summary");
const statusBox = document.querySelector("#status");
const refresh = document.querySelector("#refresh");
const back = document.querySelector("#back");
const metrics = document.querySelector("#metrics");
const overview = document.querySelector("#overview");
const list = document.querySelector("#list");
const detail = document.querySelector("#detail");
const searchInput = document.querySelector("#search");
const filtersBox = document.querySelector("#filters");

// ---------------------------------------------------------------------------
// View state
// ---------------------------------------------------------------------------
let cachedProjects = null;
let listSearch = "";
let listFilter = "all";
let currentDetail = null; // { projectID, dashboard }
const statusCache = new Map(); // projectID -> { health, runStatus }
const cardById = new Map(); // projectID -> card element

const FILTERS = [
  { id: "all", label: "All" },
  { id: "enabled", label: "Enabled" },
  { id: "attention", label: "Needs attention" },
];

const TABS = [
  { id: "overview", label: "Overview" },
  { id: "graph", label: "Graph" },
  { id: "ingestion", label: "Ingestion" },
  { id: "workspace", label: "Workspace" },
  { id: "integrations", label: "Integrations" },
];

const DONUT_COLORS = ["var(--c1)", "var(--c2)", "var(--c3)", "var(--c4)"];
const SVGNS = "http://www.w3.org/2000/svg";

// Live sync polling: there is no total-files denominator in the run metadata
// (files_seen is a running processed count, not a target), so we never show a
// fabricated percentage — only honest live counters while a run is active.
const POLL_MS = 3000;
const ACTIVE_HEALTH = new Set(["syncing", "running", "warming_up"]);
const ACTIVE_RUN = new Set(["running", "queued", "pending", "syncing"]);
let pollHandle = null;
let activitySource = null;
let activityDrawer = null;
let activityList = null;
let activityStatus = null;
let activityEvents = [];

// ---------------------------------------------------------------------------
// Bootstrap
// ---------------------------------------------------------------------------
refresh.addEventListener("click", () => {
  currentDetail = null;
  cachedProjects = null;
  statusCache.clear();
  loadCurrentView();
});
back.addEventListener("click", () => {
  location.hash = "";
});
window.addEventListener("hashchange", loadCurrentView);
searchInput.addEventListener("input", (event) => {
  listSearch = event.target.value.trim().toLowerCase();
  renderListView();
  loadOptionalStatus(visibleProjects());
});

loadCurrentView();

// ---------------------------------------------------------------------------
// DOM builders (CSP-safe: dynamic styling is applied through the CSSOM, never
// through inline style attributes, which the dashboard CSP forbids).
// ---------------------------------------------------------------------------
function el(tag, props, ...children) {
  const node = document.createElement(tag);
  if (props) {
    for (const [key, value] of Object.entries(props)) {
      if (value == null || value === false) continue;
      if (key === "class") node.className = value;
      else if (key === "text") node.textContent = value;
      else if (key === "tabIndex") node.tabIndex = value;
      else if (key === "dataset") {
        for (const [dk, dv] of Object.entries(value)) node.dataset[dk] = dv;
      } else if (key === "style" && typeof value === "object") {
        for (const [prop, val] of Object.entries(value)) {
          if (val != null) node.style.setProperty(prop, String(val));
        }
      } else if (key.startsWith("on") && typeof value === "function") {
        node.addEventListener(key.slice(2).toLowerCase(), value);
      } else {
        node.setAttribute(key, value);
      }
    }
  }
  appendChildren(node, children);
  return node;
}

function svgEl(tag, attrs, ...children) {
  const node = document.createElementNS(SVGNS, tag);
  if (attrs) {
    for (const [key, value] of Object.entries(attrs)) {
      if (value != null) node.setAttribute(key, String(value));
    }
  }
  appendChildren(node, children);
  return node;
}

function frag(...children) {
  const fragment = document.createDocumentFragment();
  appendChildren(fragment, children);
  return fragment;
}

function appendChildren(node, children) {
  for (const child of children.flat(Infinity)) {
    if (child == null || child === false) continue;
    node.append(child.nodeType ? child : document.createTextNode(String(child)));
  }
}

function clear(node) {
  node.replaceChildren();
}

// ---------------------------------------------------------------------------
// Routing
// ---------------------------------------------------------------------------
function loadCurrentView() {
  const { projectID, tab } = selectedRoute();
  if (projectID) {
    if (currentDetail && currentDetail.projectID === projectID) {
      showTab(tab);
      return;
    }
    loadProjectDetail(projectID);
    return;
  }
  currentDetail = null;
  loadDashboard();
}

function selectedRoute() {
  const hash = location.hash.replace(/^#/, "");
  const params = new URLSearchParams(hash);
  return { projectID: params.get("project") || "", tab: params.get("tab") || "overview" };
}

function openProject(id) {
  location.hash = `project=${encodeURIComponent(id)}`;
}

function selectTab(tab) {
  const { projectID } = selectedRoute();
  if (!projectID) return;
  location.hash = `project=${encodeURIComponent(projectID)}&tab=${encodeURIComponent(tab)}`;
}

// ---------------------------------------------------------------------------
// Projects list (card grid)
// ---------------------------------------------------------------------------
async function loadDashboard() {
  stopPolling();
  closeActivityDrawer();
  refresh.disabled = true;
  back.classList.add("hidden");
  statusBox.textContent = "";
  detail.classList.add("hidden");
  clear(detail);
  overview.classList.remove("hidden");
  summary.textContent = "Loading projects";
  clear(list);
  metrics.replaceChildren();

  try {
    const data = await fetchJSON("/api/v1/projects", 4000);
    const projects = Array.isArray(data.projects) ? data.projects : [];
    cachedProjects = projects;
    summary.textContent = `${projects.length} configured project${projects.length === 1 ? "" : "s"}`;
    renderMetrics(projects);
    renderFilters();
    renderListView();
    await loadOptionalStatus(visibleProjects());
  } catch (error) {
    summary.textContent = "Projects unavailable";
    statusBox.textContent = error.message;
  } finally {
    refresh.disabled = false;
  }
}

function renderMetrics(projects) {
  const enabled = projects.filter((project) => project.enabled).length;
  const valid = projects.filter((project) => project.validation_status === "valid").length;
  const live = projects.filter((project) => project.update_policy === "live").length;
  const editable = projects.filter((project) => project.workspace_mode === "edit").length;

  metrics.replaceChildren(
    metricCard("Projects", projects.length, `${enabled} enabled`),
    metricCard("Validation", valid, `${projects.length - valid} need attention`),
    metricCard("Live graphs", live, `${projects.length - live} manual or disabled`),
    metricCard("Workspace edit", editable, `${projects.length - editable} read-only`),
  );
}

function metricCard(label, value, note) {
  return el("article", { class: "metric" },
    el("span", { class: "metric__label", text: label }),
    el("strong", { class: "metric__value", text: String(value) }),
    el("small", { class: "metric__note", text: note }),
  );
}

function renderFilters() {
  clear(filtersBox);
  FILTERS.forEach((filter) => {
    const count = (cachedProjects || []).filter((project) => matchesFilter(project, filter.id)).length;
    const active = listFilter === filter.id;
    filtersBox.append(el("button", {
      class: "chip" + (active ? " is-active" : ""),
      type: "button",
      "aria-pressed": active ? "true" : "false",
      onClick: () => {
        listFilter = filter.id;
        renderFilters();
        renderListView();
        loadOptionalStatus(visibleProjects());
      },
    }, filter.label, el("span", { class: "chip__count", text: String(count) })));
  });
}

function visibleProjects() {
  if (!Array.isArray(cachedProjects)) return [];
  return cachedProjects.filter((project) => matchesFilter(project, listFilter) && matchesSearch(project));
}

function matchesFilter(project, filterID) {
  switch (filterID) {
    case "enabled":
      return Boolean(project.enabled);
    case "attention":
      return !project.enabled || project.validation_status !== "valid";
    default:
      return true;
  }
}

function matchesSearch(project) {
  if (!listSearch) return true;
  const haystack = [project.display_name, project.id, ...(project.aliases || [])]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
  return haystack.includes(listSearch);
}

function renderListView() {
  cardById.clear();
  clear(list);
  if (!cachedProjects || cachedProjects.length === 0) {
    list.append(emptyText("No projects configured."));
    return;
  }
  const projects = visibleProjects();
  if (projects.length === 0) {
    list.append(emptyText("No projects match the current filter."));
    return;
  }
  projects.forEach((project) => {
    const card = projectCard(project);
    cardById.set(project.id, card);
    list.append(card);
    const cached = statusCache.get(project.id);
    if (cached) applyCardStatus(project.id, cached.health, cached.runStatus);
  });
}

function projectCard(project) {
  const card = el("button", {
    class: "project-card",
    type: "button",
    dataset: { projectId: project.id },
    onClick: () => openProject(project.id),
  },
    el("div", { class: "card__head" },
      el("strong", { class: "card__name", text: project.display_name || project.id }),
      el("span", { class: "card__id", text: project.id }),
    ),
    el("div", { class: "card__badges" }, projectEnabledPill(project), projectValidationPill(project)),
    el("div", { class: "card__status" }, el("span", { class: "muted-text", text: "Checking status…" })),
    el("div", { class: "card-kpis" }),
    el("div", { class: "card__meta" },
      metaItem("Graph", `${project.graph_storage} · ${project.digest_mode}/${project.update_policy}`),
      metaItem("Workspace", project.workspace_mode || "unknown"),
    ),
    integrationsInline(project.integrations),
    aliasesInline(project),
  );
  return card;
}

function metaItem(label, value) {
  return el("div", { class: "meta" },
    el("span", { class: "meta__label", text: label }),
    el("span", { class: "meta__value", text: value }),
  );
}

async function loadOptionalStatus(projects) {
  await Promise.all(projects.map(async (project) => {
    const cached = statusCache.get(project.id);
    if (cached) {
      applyCardStatus(project.id, cached.health, cached.runStatus);
      return;
    }
    try {
      const [health, latest] = await Promise.allSettled([
        fetchJSON(`/api/v1/projects/${encodeURIComponent(project.id)}/context-health`, 3000),
        fetchJSON(`/api/v1/projects/${encodeURIComponent(project.id)}/ingestion-runs/latest`, 3000),
      ]);
      const healthValue = health.status === "fulfilled" ? health.value : null;
      const runStatus = latest.status === "fulfilled" && latest.value.status ? latest.value.status : "";
      statusCache.set(project.id, { health: healthValue, runStatus });
      applyCardStatus(project.id, healthValue, runStatus);
    } catch {
      const card = cardById.get(project.id);
      const slot = card && card.querySelector(".card__status");
      if (slot) slot.replaceChildren(el("span", { class: "muted-text", text: "status unavailable" }));
    }
  }));
}

function applyCardStatus(projectID, health, runStatus) {
  const card = cardById.get(projectID);
  if (!card) return;
  const slot = card.querySelector(".card__status");
  if (slot) slot.replaceChildren(contextHealthPill(health), latestRunPill(runStatus));
  const kpis = card.querySelector(".card-kpis");
  if (kpis) {
    kpis.replaceChildren(
      kpiMini("Files", numberValue(health?.eligible_file_count)),
      kpiMini("Symbols", numberValue(health?.indexed_symbol_count)),
      kpiMini("Chunks", numberValue(health?.indexed_chunk_count)),
    );
  }
}

function kpiMini(label, value) {
  return el("div", { class: "kpi-mini" },
    el("span", { class: "kpi-mini__value", text: value }),
    el("span", { class: "kpi-mini__label", text: label }),
  );
}

// ---------------------------------------------------------------------------
// Project detail (tabbed)
// ---------------------------------------------------------------------------
async function loadProjectDetail(projectID) {
  stopPolling();
  refresh.disabled = true;
  back.classList.remove("hidden");
  statusBox.textContent = "";
  overview.classList.add("hidden");
  detail.classList.remove("hidden");
  clear(detail);
  detail.append(el("section", { class: "panel loading", text: "Loading project details…" }));
  summary.textContent = projectID;

  try {
    const dashboard = await fetchJSON(`/api/v1/projects/${encodeURIComponent(projectID)}/dashboard-summary`, 12000);
    currentDetail = { projectID, dashboard };
    summary.textContent = dashboard.project.display_name || dashboard.project.id;
    renderProjectDetail(dashboard);
  } catch (error) {
    currentDetail = null;
    summary.textContent = "Project unavailable";
    statusBox.textContent = error.message;
    clear(detail);
    detail.append(el("section", { class: "panel empty", text: "Project details could not be loaded." }));
  } finally {
    refresh.disabled = false;
  }
}

function renderProjectDetail(dashboard) {
  const project = dashboard.project;
  const health = dashboard.context_health;
  const latest = dashboard.latest_run;
  const graph = dashboard.graph || {};
  clear(detail);
  appendChildren(detail, [
    el("div", { class: "detail-head" }),
    buildTabs(project, health, latest, graph, dashboard),
  ]);
  renderHead();
  showTab(selectedRoute().tab);
  managePolling();
}

// Re-renders only the header region (hero, sync banner, KPI strip, warnings)
// so live polling updates never disturb the active tab or its scroll position.
function renderHead() {
  const head = detail.querySelector(".detail-head");
  const dashboard = currentDetail && currentDetail.dashboard;
  if (!head || !dashboard) return;
  const project = dashboard.project;
  const health = dashboard.context_health;
  const latest = dashboard.latest_run;
  const graph = dashboard.graph || {};
  clear(head);
  appendChildren(head, [
    hero(project, health),
    syncBanner(health, latest),
    kpiStrip(health, graph, latest),
    warningsBlock(dashboard.warnings),
  ]);
}

// ---------------------------------------------------------------------------
// Live sync progress
// ---------------------------------------------------------------------------
function isActive(health, latest) {
  return ACTIVE_HEALTH.has(health?.status) || ACTIVE_RUN.has(latest?.status);
}

function syncBanner(health, latest) {
  const healthActive = ACTIVE_HEALTH.has(health?.status);
  const runActive = ACTIVE_RUN.has(latest?.status);
  if (!healthActive && !runActive) return null;
  const phase = runActive && latest.current_phase ? latest.current_phase : (health?.status || "syncing");
  const children = [
    el("div", { class: "sync__head" },
      pill("syncing", "warn"),
      el("span", { class: "sync__phase", text: phase }),
    ),
    el("div", { class: "progress progress--indeterminate" }, el("i", { class: "progress__bar" })),
  ];
  if (runActive) {
    children.push(el("div", { class: "sync__stats" },
      syncStat(numberValue(latest.files_seen), "files processed"),
      syncStat(numberValue(latest.chunks_stored), "chunks"),
      syncStat(numberValue(latest.symbols_stored), "symbols"),
    ));
    const age = progressAge(latest.last_progress_at || latest.heartbeat_at);
    children.push(el("span", { class: "sync__age", text: age || "no total known — count is files handled so far" }));
  } else {
    children.push(el("span", { class: "sync__age", text: "preparing to index…" }));
  }
  return el("section", { class: "sync panel", "aria-live": "polite" }, ...children);
}

function syncStat(value, label) {
  return el("div", { class: "sync-stat" },
    el("strong", { class: "sync-stat__value", text: value }),
    el("span", { class: "sync-stat__label", text: label }),
  );
}

function progressAge(value) {
  if (!value) return "";
  const then = new Date(value).getTime();
  if (Number.isNaN(then)) return "";
  const seconds = Math.max(0, Math.round((Date.now() - then) / 1000));
  if (seconds < 60) return `last progress ${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `last progress ${minutes}m ${seconds % 60}s ago`;
  return `last progress ${Math.floor(minutes / 60)}h ${minutes % 60}m ago`;
}

function managePolling() {
  stopPolling();
  if (!currentDetail) return;
  const dashboard = currentDetail.dashboard;
  if (isActive(dashboard.context_health, dashboard.latest_run)) {
    pollHandle = setTimeout(pollOnce, POLL_MS);
  }
}

function stopPolling() {
  if (pollHandle) {
    clearTimeout(pollHandle);
    pollHandle = null;
  }
}

async function pollOnce() {
  pollHandle = null;
  if (!currentDetail) return;
  const projectID = currentDetail.projectID;
  if (selectedRoute().projectID !== projectID) return;

  let health = null;
  let latest = null;
  try {
    const [healthResult, latestResult] = await Promise.allSettled([
      fetchJSON(`/api/v1/projects/${encodeURIComponent(projectID)}/context-health`, 3000),
      fetchJSON(`/api/v1/projects/${encodeURIComponent(projectID)}/ingestion-runs/latest`, 3000),
    ]);
    if (healthResult.status === "fulfilled") health = healthResult.value;
    if (latestResult.status === "fulfilled" && latestResult.value && latestResult.value.status) latest = latestResult.value;
  } catch {
    pollHandle = setTimeout(pollOnce, POLL_MS);
    return;
  }
  if (!currentDetail || currentDetail.projectID !== projectID) return;

  const wasActive = isActive(currentDetail.dashboard.context_health, currentDetail.dashboard.latest_run);
  if (health) currentDetail.dashboard.context_health = health;
  if (latest) currentDetail.dashboard.latest_run = latest;
  const stillActive = isActive(currentDetail.dashboard.context_health, currentDetail.dashboard.latest_run);

  renderHead();
  if (stillActive) {
    pollHandle = setTimeout(pollOnce, POLL_MS);
  } else if (wasActive) {
    // Run just finished: reload the full summary so graph stats, composition
    // and AST coverage reflect the freshly indexed content.
    loadProjectDetail(projectID);
  }
}

function hero(project, health) {
  return el("section", { class: "hero" },
    el("div", { class: "hero__main" },
      el("span", { class: "eyebrow", text: "Project" }),
      el("h2", { class: "hero__title", text: project.display_name || project.id }),
      el("p", { class: "hero__desc", text: project.description || "No description configured" }),
    ),
    el("div", { class: "hero__status" },
      el("button", {
        class: "secondary compact",
        type: "button",
        title: "Show live MCP activity for this project.",
        onClick: () => openActivityDrawer(project.id),
      }, "Agent activity"),
      contextHealthPill(health),
      projectValidationPill(project),
      projectEnabledPill(project),
    ),
  );
}

function kpiStrip(health, graph, latest) {
  const items = [
    ["Files", numberValue(health?.eligible_file_count ?? graph?.files?.sampled_count)],
    ["Symbols", numberValue(health?.indexed_symbol_count)],
    ["Chunks", numberValue(health?.indexed_chunk_count)],
    ["Headings", numberValue(graph?.headings?.sampled_count)],
    ["Health", contextStatusLabel(health)],
    ["Last run", latest?.status || "unknown"],
  ];
  return el("div", { class: "kpi-strip" },
    items.map(([label, value]) => el("div", { class: "kpi" },
      el("span", { class: "kpi__label", text: label }),
      el("strong", { class: "kpi__value", text: value }),
    )),
  );
}

// ---------------------------------------------------------------------------
// Agent activity drawer
// ---------------------------------------------------------------------------
function openActivityDrawer(projectID) {
  closeActivityDrawer();
  activityEvents = [];
  activityDrawer = el("aside", { class: "activity-drawer", role: "dialog", "aria-modal": "false", "aria-label": "Agent activity" },
    el("div", { class: "activity-drawer__head" },
      el("div", {},
        el("span", { class: "eyebrow", text: "MCP" }),
        el("h2", { class: "activity-drawer__title", text: "Agent activity" }),
      ),
      el("button", { class: "secondary compact", type: "button", onClick: closeActivityDrawer, title: "Close activity drawer" }, "Close"),
    ),
    el("p", { class: "activity-drawer__note", text: "Raw payloads are available inside collapsed details and are not downloaded automatically." }),
    el("div", { class: "activity-toolbar" },
      el("button", { class: "secondary compact", type: "button", onClick: () => renderActivityEvents([]) }, "Clear"),
      el("button", { class: "secondary compact", type: "button", onClick: copyVisibleActivity }, "Copy JSONL"),
    ),
    activityStatus = el("div", { class: "activity-status", text: "Connecting..." }),
    activityList = el("div", { class: "activity-list" }),
  );
  document.body.append(activityDrawer);
  activitySource = new EventSource(`/api/v1/projects/${encodeURIComponent(projectID)}/agent-activity/stream?recent=50`);
  activitySource.addEventListener("open", () => {
    if (activityStatus) activityStatus.textContent = "Connected";
  });
  activitySource.addEventListener("mcp_activity", (event) => {
    try {
      activityEvents.push(JSON.parse(event.data));
      if (activityEvents.length > 200) activityEvents = activityEvents.slice(-200);
      renderActivityEvents(activityEvents);
    } catch {
      if (activityStatus) activityStatus.textContent = "Received malformed activity event";
    }
  });
  activitySource.addEventListener("error", () => {
    if (activityStatus) activityStatus.textContent = "Stream disconnected or unavailable";
  });
}

function closeActivityDrawer() {
  if (activitySource) {
    activitySource.close();
    activitySource = null;
  }
  if (activityDrawer) {
    activityDrawer.remove();
    activityDrawer = null;
  }
  activityList = null;
  activityStatus = null;
  activityEvents = [];
}

function renderActivityEvents(events) {
  activityEvents = events;
  if (!activityList) return;
  clear(activityList);
  if (!events.length) {
    activityList.append(emptyText("No MCP activity captured for this project yet."));
    return;
  }
  events.slice().reverse().forEach((event) => activityList.append(activityEventRow(event)));
}

function activityEventRow(event) {
  const statusTone = event.status === "ok" ? "ok" : "warn";
  return el("article", { class: "activity-event" },
    el("div", { class: "activity-event__summary" },
      el("div", { class: "activity-event__main" },
        el("strong", { text: event.tool_name || event.method || "mcp" }),
        el("span", { class: "muted-text", text: formatDate(event.timestamp) }),
      ),
      el("div", { class: "activity-event__badges" },
        pill(event.status || "unknown", statusTone),
        el("span", { class: "tag", text: `${numberValue(event.duration_ms)} ms` }),
      ),
    ),
    event.error ? el("p", { class: "activity-event__error", text: event.error }) : null,
    el("details", { class: "activity-details activity-details--summary" },
      el("summary", { text: "Call summary" }),
      el("div", { class: "activity-summary-grid" },
        activityPayloadBlock("Inputs", event.raw_arguments || event.raw_params || {}),
        activityPayloadBlock("Outputs", event.raw_result || (event.error ? { error: event.error } : {})),
      ),
    ),
    el("details", { class: "activity-details" },
      el("summary", { text: "Full payload" }),
      el("pre", { text: prettyJSON({
        id: event.id,
        request_id: event.request_id,
        project_id: event.project_id,
        method: event.method,
        tool_name: event.tool_name,
        remote_addr: event.remote_addr,
        user_agent: event.user_agent,
        raw_request: event.raw_request,
        raw_params: event.raw_params,
        raw_arguments: event.raw_arguments,
        raw_result: event.raw_result,
      }) }),
    ),
  );
}

function activityPayloadBlock(title, payload) {
  return el("div", { class: "activity-payload" },
    el("strong", { text: title }),
    el("pre", { text: prettyJSON(payload) }),
  );
}

function copyVisibleActivity() {
  if (!activityEvents.length || !navigator.clipboard) return;
  const jsonl = activityEvents.map((event) => JSON.stringify(event)).join("\n");
  navigator.clipboard.writeText(jsonl).then(() => {
    if (activityStatus) activityStatus.textContent = "Copied visible activity as JSONL";
  }).catch(() => {
    if (activityStatus) activityStatus.textContent = "Copy failed";
  });
}

function buildTabs(project, health, latest, graph, dashboard) {
  const renderers = {
    overview: () => tabOverview(project, health, latest, graph),
    graph: () => tabGraph(graph),
    ingestion: () => tabIngestion(latest, graph),
    workspace: () => tabWorkspace(dashboard.workspace),
    integrations: () => tabIntegrations(project, dashboard.integrations),
  };
  const tablist = el("div", { class: "tabs", role: "tablist", "aria-label": "Project sections" });
  const panels = el("div", { class: "tab-panels" });
  TABS.forEach((tab) => {
    tablist.append(el("button", {
      class: "tab",
      type: "button",
      role: "tab",
      id: `tab-${tab.id}`,
      "aria-controls": `panel-${tab.id}`,
      text: tab.label,
      onClick: () => selectTab(tab.id),
    }));
    panels.append(el("section", {
      class: "tab-panel",
      role: "tabpanel",
      id: `panel-${tab.id}`,
      "aria-labelledby": `tab-${tab.id}`,
      tabIndex: 0,
    }, renderers[tab.id]()));
  });
  tablist.addEventListener("keydown", onTablistKeydown);
  return el("div", { class: "tabbed" }, tablist, panels);
}

function showTab(tabID) {
  const valid = TABS.some((tab) => tab.id === tabID) ? tabID : "overview";
  TABS.forEach((tab) => {
    const button = document.getElementById(`tab-${tab.id}`);
    const panel = document.getElementById(`panel-${tab.id}`);
    if (!button || !panel) return;
    const active = tab.id === valid;
    button.classList.toggle("is-active", active);
    button.setAttribute("aria-selected", active ? "true" : "false");
    button.tabIndex = active ? 0 : -1;
    panel.hidden = !active;
  });
}

function onTablistKeydown(event) {
  if (!["ArrowRight", "ArrowLeft", "Home", "End"].includes(event.key)) return;
  event.preventDefault();
  const ids = TABS.map((tab) => tab.id);
  let index = ids.indexOf(selectedRoute().tab);
  if (index < 0) index = 0;
  if (event.key === "ArrowRight") index = (index + 1) % ids.length;
  else if (event.key === "ArrowLeft") index = (index - 1 + ids.length) % ids.length;
  else if (event.key === "Home") index = 0;
  else if (event.key === "End") index = ids.length - 1;
  selectTab(ids[index]);
  const button = document.getElementById(`tab-${ids[index]}`);
  if (button) button.focus();
}

// ---------------------------------------------------------------------------
// Tab content
// ---------------------------------------------------------------------------
function tabOverview(project, health, latest, graph) {
  const identity = panel("Project",
    infoList([
      ["Project ID", project.id],
      ["Classification", project.classification || "unknown"],
      ["Workspace", project.workspace_mode || "unknown"],
      ["Digest mode", project.digest_mode || "unknown"],
      ["Update policy", project.update_policy || "unknown"],
      ["Graph storage", project.graph_storage || "unknown"],
    ]),
    aliasesInline(project) || el("p", { class: "muted-text", text: "No aliases configured." }),
  );
  const compItems = [
    { key: "Files", count: health?.eligible_file_count ?? graph?.files?.sampled_count ?? 0 },
    { key: "Chunks", count: health?.indexed_chunk_count ?? 0 },
    { key: "Symbols", count: health?.indexed_symbol_count ?? 0 },
    { key: "Headings", count: graph?.headings?.sampled_count ?? 0 },
  ];
  const composition = panel("Graph composition",
    el("div", { class: "composition" }, donutVisual(compItems), distributionBars(compItems)),
  );
  return frag(
    identity,
    panel("Data pipeline", stageFlow(project, health, latest, graph)),
    composition,
    panel("Context health", contextHealthBlock(project, health)),
  );
}

function tabGraph(graph) {
  const distributions = el("div", { class: "grid" },
    countBlock("Files by extension", graph?.files?.by_extension),
    countBlock("Symbols by kind", graph?.symbols?.by_kind),
    countBlock("Symbols by language", graph?.symbols?.by_language),
    countBlock("Headings by level", graph?.headings?.by_level),
  );
  const concentration = concentrationBlocks(graph?.symbols);
  return frag(
    panel("Graph distributions", distributions),
    panel("Symbol concentration",
      concentration.length
        ? el("div", { class: "grid" }, ...concentration)
        : emptyText("No concentration data.")),
  );
}

function tabIngestion(latest, graph) {
  return frag(
    panel("Latest ingestion", latestRunBlock(latest)),
    panel("Ingestion timeline", timelineBlock(latest)),
    panel("AST coverage", astCoverageBlock(graph?.ast_coverage)),
  );
}

function tabWorkspace(workspace) {
  if (!workspace) return panel("Workspace", emptyText("Workspace git status unavailable."));
  return panel("Workspace",
    el("div", { class: "grid" },
      infoList([
        ["Branch", workspace.branch || "unknown"],
        ["Head", workspace.head_oid_short || "unknown"],
        ["Dirty sampled", numberValue(workspace.sampled_dirty_count)],
        ["Truncated", workspace.truncated ? "yes" : "no"],
      ]),
      countBlock("Dirty by status", workspace.by_status),
    ),
    block("Working tree sample", gitSampleList(workspace.sample)),
  );
}

function tabIntegrations(project, integrationSummary) {
  const providers = Array.isArray(integrationSummary?.providers) ? integrationSummary.providers : [];
  const counts = Array.isArray(integrationSummary?.counts) ? integrationSummary.counts : [];
  const children = [
    block("Configured", integrationsInline(project.integrations) || emptyText("No integrations configured.")),
    block("Provider status",
      providers.length
        ? el("div", { class: "grid grid--tight" }, providers.map((provider) => providerTile(provider, counts)))
        : emptyText("No live integration status returned.")),
  ];
  if (counts.length) {
    children.push(countBlock("Indexed integration items", counts.map((item) => ({ key: item.provider, count: item.count })), { total: 0 }));
  }
  return panel("Integrations", ...children);
}

// ---------------------------------------------------------------------------
// Detail building blocks
// ---------------------------------------------------------------------------
function stageFlow(project, health, latest, graph) {
  const chunks = health?.indexed_chunk_count ?? latest?.chunks_stored ?? 0;
  const symbols = health?.indexed_symbol_count ?? latest?.symbols_stored ?? 0;
  const searchStatus = graph?.search_index?.status || health?.search_index?.status || "unknown";
  const scan = latest?.status || health?.latest_run?.status || "unavailable";
  const stages = [
    ["Config", project.enabled ? (project.validation_status || "enabled") : "disabled",
      project.enabled && project.validation_status === "valid" ? "ok" : "warn"],
    ["Scan", scan, scan === "completed" ? "ok" : "warn"],
    ["Store", `${numberValue(chunks)} chunks`, chunks > 0 ? "ok" : "muted"],
    ["Index", `${numberValue(symbols)} symbols`, symbols > 0 ? "ok" : "muted"],
    ["Search", searchStatus, searchStatus === "ok" ? "ok" : "warn"],
    ["Serve", health?.status || "unknown", health?.indexed_content_available ? "ok" : "warn"],
  ];
  return el("div", { class: "flow" },
    stages.map(([label, value, tone]) => el("div", { class: "flow__node" },
      pill(label, tone),
      el("strong", { class: "flow__val", text: value }),
    )),
  );
}

function donutVisual(items) {
  const values = normalizeCounts(items).filter((item) => item.count > 0);
  const total = values.reduce((sum, item) => sum + item.count, 0);
  if (!total) return emptyText("No graph totals.");
  let offset = 25;
  const rings = values.map((item, index) => {
    const length = (item.count / total) * 100;
    const ring = svgEl("circle", {
      cx: 58, cy: 58, r: 46, fill: "none",
      "stroke-width": 14,
      "stroke-dasharray": `${length} ${100 - length}`,
      "stroke-dashoffset": offset,
      pathLength: 100,
    });
    ring.style.setProperty("stroke", DONUT_COLORS[index % DONUT_COLORS.length]);
    offset -= length;
    return ring;
  });
  const track = svgEl("circle", { cx: 58, cy: 58, r: 46, fill: "none", "stroke-width": 14 });
  track.style.setProperty("stroke", "var(--bar-track)");
  const svg = svgEl("svg", { viewBox: "0 0 116 116", width: 116, height: 116, role: "img", "aria-label": "Graph composition donut" },
    track,
    ...rings,
    svgEl("text", { x: 58, y: 55, "text-anchor": "middle", fill: "currentColor", "font-size": 15, "font-weight": 700 }, numberValue(total)),
    svgEl("text", { x: 58, y: 72, "text-anchor": "middle", fill: "currentColor", opacity: 0.64, "font-size": 10 }, "total"),
  );
  const legend = el("div", { class: "donut__legend" },
    values.map((item, index) => {
      const dot = el("i", { class: "donut__dot" });
      dot.style.setProperty("background", DONUT_COLORS[index % DONUT_COLORS.length]);
      return el("span", { class: "donut__item" }, dot, `${item.key}: ${numberValue(item.count)}`);
    }),
  );
  return el("div", { class: "donut" }, svg, legend);
}

function distributionBars(items, options = {}) {
  const values = normalizeCounts(items);
  if (!values.length) return emptyText("No data.");
  const max = Math.max(...values.map((item) => item.count), 1);
  const total = Number.isFinite(options.total) && options.total > 0 ? options.total : 0;
  return el("div", { class: "bars" },
    values.map((item) => {
      const width = Math.max(2, Math.round((item.count / max) * 100));
      const countLabel = total ? `${numberValue(item.count)} · ${percent(item.count, total)}` : numberValue(item.count);
      const fill = el("i", { class: "bar__fill" });
      fill.style.setProperty("--w", `${width}%`);
      return el("div", { class: "bar" },
        el("div", { class: "bar__head" },
          el("span", { class: "bar__key", text: item.key }),
          el("strong", { class: "bar__val", text: countLabel }),
        ),
        el("div", { class: "bar__track" }, fill),
      );
    }),
  );
}

function countBlock(title, items, options = {}) {
  const node = block(title);
  if (!Array.isArray(items) || items.length === 0) {
    node.append(emptyText("No data."));
    return node;
  }
  node.append(distributionBars(items, options));
  return node;
}

function contextHealthBlock(project, health) {
  if (!health) return emptyText("Context health unavailable.");
  const reasons = Object.entries(health.reason_counts || {});
  return el("div", { class: "grid" },
    infoList([
      ["Status", contextStatusLabel(health)],
      ["Reason", health.status_reason || "none"],
      ["Digest mode", project.digest_mode || "unknown"],
      ["Update policy", project.update_policy || "unknown"],
      ["Indexed content", health.indexed_content_available ? "available" : "unavailable"],
      ["Last checked", formatDate(health.checked_at)],
    ]),
    block("Skipped reasons",
      reasons.length
        ? el("div", { class: "rows" }, reasons.map(([key, value]) => reasonRow(key, value)))
        : emptyText("No skipped reason counts.")),
  );
}

function reasonRow(key, value) {
  return el("div", { class: "row" },
    el("span", { text: key }),
    el("strong", { text: numberValue(value) }),
  );
}

function latestRunBlock(run) {
  if (!run) return emptyText("Latest ingestion run unavailable.");
  return el("div", { class: "grid" },
    infoList([
      ["Run ID", run.id || "unknown"],
      ["Status", run.status || "unknown"],
      ["Trigger", run.trigger || "unknown"],
      ["Mode", run.mode || "unknown"],
      ["Phase", run.current_phase || "unknown"],
      ["Started", formatDate(run.started_at)],
      ["Finished", formatDate(run.finished_at)],
    ]),
    infoList([
      ["Files seen", numberValue(run.files_seen)],
      ["Ingested", numberValue(run.files_ingested)],
      ["Skipped", numberValue(run.files_skipped)],
      ["Unchanged", numberValue(run.files_unchanged)],
      ["Chunks", numberValue(run.chunks_stored)],
      ["Symbols", numberValue(run.symbols_stored)],
    ]),
  );
}

function timelineBlock(run) {
  if (!run) return emptyText("Latest ingestion timeline unavailable.");
  const points = [
    ["Started", run.started_at],
    ["Progress", run.last_progress_at || run.heartbeat_at],
    ["Heartbeat", run.heartbeat_at],
    ["Finished", run.finished_at],
  ].filter(([, value]) => value);
  if (!points.length) return emptyText("Latest ingestion timestamps unavailable.");
  return el("div", { class: "timeline" },
    points.map(([label, value]) => el("div", { class: "timeline-item" },
      el("strong", { text: label }),
      el("span", { text: formatDate(value) }),
    )),
  );
}

function astCoverageBlock(coverage) {
  if (!Array.isArray(coverage) || coverage.length === 0) return emptyText("AST coverage unavailable.");
  return el("div", { class: "grid grid--tight" },
    coverage.map((item) => {
      const eligible = item.eligible_files ?? 0;
      const skipped = item.skipped_file_too_large ?? 0;
      const complete = item.coverage_status === "complete";
      return el("div", { class: "tile" },
        el("div", { class: "tile__head" },
          el("strong", { text: item.language || "unknown" }),
          pill(item.coverage_status || "unknown", complete ? "ok" : "warn"),
        ),
        el("span", { class: "tile__sub", text: `${numberValue(eligible)} eligible · ${numberValue(skipped)} oversized skips` }),
        el("span", { class: "tile__sub", text: (item.extensions || []).join(", ") || "no extensions" }),
      );
    }),
  );
}

function gitSampleList(items) {
  if (!Array.isArray(items) || items.length === 0) return emptyText("No working tree changes.");
  return el("div", { class: "rows" },
    items.map((item) => el("div", { class: "row row--file" },
      el("strong", { text: item.relative_path || "(unknown path)" }),
      el("span", { text: item.status || "unknown" }),
    )),
  );
}

function providerTile(provider, counts) {
  const name = provider.provider || provider.name || "unknown";
  const count = counts.find((item) => item.provider === name)?.count;
  const scopes = provider.allowlist_count ?? provider.project_key_count ?? provider.space_key_count ?? 0;
  const detail = `${provider.allowlist_kind || "allowlist"} ${numberValue(scopes)} scopes · source ${provider.source_persisted ? "persisted" : "not persisted"}` +
    (typeof count === "number" ? ` · ${numberValue(count)} local items` : "");
  return el("div", { class: "tile" },
    el("div", { class: "tile__head" },
      el("strong", { text: name }),
      pill(provider.enabled ? "enabled" : "disabled", provider.enabled ? "ok" : "muted"),
    ),
    el("div", { class: "tile__badges" }, pill(provider.ingestion_enabled ? "ingest on" : "ingest off", provider.ingestion_enabled ? "ok" : "muted")),
    el("span", { class: "tile__sub", text: detail }),
  );
}

function integrationsInline(integrations) {
  if (!integrations) return null;
  const parts = [];
  if (integrations.jira) parts.push(providerInline("Jira", integrations.jira));
  if (integrations.confluence) parts.push(providerInline("Confluence", integrations.confluence));
  if (!parts.length) return null;
  return el("div", { class: "inline-providers" }, ...parts);
}

function providerInline(name, value) {
  const count = value.project_key_count || value.space_key_count || 0;
  return el("div", { class: "inline-provider" },
    el("span", { class: "inline-provider__name", text: name }),
    pill(value.enabled ? "on" : "off", value.enabled ? "ok" : "muted"),
    el("span", { class: "inline-provider__meta", text: `${count} scopes · ${value.ingestion_enabled ? "ingest on" : "ingest off"}` }),
  );
}

function aliasesInline(project) {
  if (!Array.isArray(project.aliases) || project.aliases.length === 0) return null;
  return el("div", { class: "tag-list" }, project.aliases.map((alias) => el("span", { class: "tag", text: alias })));
}

function warningsBlock(warnings) {
  if (!Array.isArray(warnings) || warnings.length === 0) return null;
  return el("section", { class: "notice" },
    el("span", { class: "notice__label", text: "Partial data" }),
    el("div", { class: "tag-list" }, warnings.map((warning) => el("span", { class: "tag", text: warning }))),
  );
}

// ---------------------------------------------------------------------------
// Symbol concentration (semantic basis: module / namespace / assembly /
// package / code area) — renders every applicable basis with its own
// denominator so percentages are comparable across languages.
// ---------------------------------------------------------------------------
function concentrationBlocks(symbols) {
  if (!symbols || typeof symbols !== "object") return [];
  const basis = symbols.concentration_basis || {};
  return symbolConcentrationCandidates(symbols, basis)
    .filter((source) => source.enabled && normalizeCounts(source.items).length)
    .map((source) => countBlock(source.title, source.items, { total: source.denominatorCount }));
}

function symbolConcentrationCandidates(symbols, basis) {
  const denominatorByField = {
    [basis.primary_field]: basis.denominator_count,
    by_module: countTotal(symbols.by_module),
    by_namespace: countTotal(symbols.by_namespace),
    by_assembly: countTotal(symbols.by_assembly),
    by_package: countTotal(symbols.by_package),
    by_code_area: symbols.total_count,
  };
  const preferred = {
    by_module: {
      enabled: basis.source !== "relative_path_bucket",
      items: symbols.by_module,
      title: "Module concentration",
      denominatorCount: denominatorByField.by_module,
    },
    by_namespace: {
      enabled: true,
      items: symbols.by_namespace,
      title: "Namespace concentration",
      denominatorCount: denominatorByField.by_namespace,
    },
    by_assembly: {
      enabled: true,
      items: symbols.by_assembly,
      title: "Assembly concentration",
      denominatorCount: denominatorByField.by_assembly,
    },
    by_package: {
      enabled: true,
      items: symbols.by_package,
      title: "Package concentration",
      denominatorCount: denominatorByField.by_package,
    },
    by_code_area: {
      enabled: true,
      items: symbols.by_code_area || symbols.by_path_bucket || symbols.by_directory || symbols.by_path,
      title: "Code area concentration",
      denominatorCount: denominatorByField.by_code_area,
    },
  };
  const orderedKeys = ["by_module", "by_namespace", "by_assembly", "by_package", "by_code_area"];
  const primary = preferred[basis.primary_field];
  return primary
    ? [primary, ...orderedKeys.filter((key) => key !== basis.primary_field).map((key) => preferred[key])]
    : orderedKeys.map((key) => preferred[key]);
}

function countTotal(items) {
  return normalizeCounts(items).reduce((sum, item) => sum + item.count, 0);
}

// ---------------------------------------------------------------------------
// Shared presentational helpers
// ---------------------------------------------------------------------------
function panel(title, ...children) {
  return el("section", { class: "panel" }, el("h3", { class: "panel__title", text: title }), ...children);
}

function block(title, ...children) {
  return el("div", { class: "block" }, el("h3", { class: "block__title", text: title }), ...children);
}

function infoList(items) {
  return el("dl", { class: "info-list" },
    items.map(([label, value]) => el("div", {},
      el("dt", { text: label }),
      el("dd", { text: value }),
    )),
  );
}

function emptyText(message) {
  return el("p", { class: "empty", text: message });
}

function pill(text, tone, title) {
  return el("span", { class: `pill ${tone}`, title: title || null, text });
}

function projectEnabledPill(project) {
  const enabled = Boolean(project?.enabled);
  return pill(
    enabled ? "project enabled" : "project disabled",
    enabled ? "ok" : "muted",
    enabled ? "This project is enabled in local config." : "This project is disabled in local config.",
  );
}

function projectValidationPill(project) {
  const status = project?.validation_status || "unknown";
  const valid = status === "valid";
  return pill(
    `config ${status}`,
    valid ? "ok" : "warn",
    valid ? "Project config passed local validation." : "Project config needs attention before it can be trusted.",
  );
}

function latestRunPill(status) {
  if (!status) {
    return pill("latest run unavailable", "warn", "Latest ingestion run could not be loaded.");
  }
  const okStatuses = new Set(["completed", "succeeded", "success"]);
  const activeStatuses = new Set(["running", "queued", "pending", "syncing"]);
  const tone = okStatuses.has(status) ? "ok" : activeStatuses.has(status) ? "warn" : "muted";
  return pill(`latest run ${status}`, tone, "Most recent ingestion run status.");
}

function contextHealthPill(health) {
  if (!health?.status) {
    return pill("index unavailable", "warn", "Indexed context health could not be loaded.");
  }
  const label = health.status === "ready" ? "index ready" : `index ${health.status}`;
  const detail = health.status_reason && health.status_reason !== "none" ? ` Reason: ${health.status_reason}.` : "";
  const availability = health.indexed_content_available ? " Indexed content is available." : " Indexed content is not available.";
  return pill(label, health.status === "ready" ? "ok" : "warn", `Indexed context health.${detail}${availability}`);
}

function contextStatusLabel(health) {
  if (!health?.status) return "unavailable";
  if (health.status === "ready" && health.status_reason === "file_warnings") {
    return "ready with warnings";
  }
  return health.status;
}

function normalizeCounts(items) {
  if (!Array.isArray(items)) return [];
  return items
    .map((item) => ({
      key: String(item.key || item.provider || "unknown"),
      count: typeof item.count === "number" ? item.count : 0,
    }))
    .filter((item) => item.count >= 0);
}

function percent(value, total) {
  if (!total) return "0%";
  return `${Math.round((value / total) * 100)}%`;
}

function numberValue(value) {
  if (typeof value !== "number") return "unknown";
  return new Intl.NumberFormat().format(value);
}

function formatDate(value) {
  if (!value) return "unknown";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "unknown";
  return date.toLocaleString();
}

function prettyJSON(value) {
  return JSON.stringify(value, null, 2);
}

async function fetchJSON(url, timeoutMs) {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const response = await fetch(url, { signal: controller.signal, headers: { "Accept": "application/json" } });
    if (!response.ok) throw new Error(`${url} returned ${response.status}`);
    return await response.json();
  } catch (error) {
    if (error.name === "AbortError") throw new Error(`${url} timed out`);
    throw error;
  } finally {
    clearTimeout(timeout);
  }
}
