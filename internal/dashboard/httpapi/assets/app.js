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
  { id: "work-plan", label: "Work Plan" },
  { id: "workflows", label: "Workflows" },
  { id: "graph", label: "Graph" },
  { id: "evidence", label: "Evidence Graph" },
  { id: "confidence", label: "Confidence" },
  { id: "knowledge", label: "Knowledge Promotion" },
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
const ACTIVITY_EVENT_TYPES = ["mcp_activity", "policy_event", "agent_run_started", "agent_step", "agent_promotion", "agent_run_completed"];
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
    const dashboard = await loadProjectDetailSkeleton(projectID);
    if (selectedRoute().projectID !== projectID) {
      refresh.disabled = false;
      return;
    }
    currentDetail = { projectID, dashboard };
    summary.textContent = dashboard.project.display_name || dashboard.project.id;
    renderProjectDetail(dashboard);
  } catch (error) {
    currentDetail = null;
    summary.textContent = "Project unavailable";
    statusBox.textContent = error.message;
    clear(detail);
    detail.append(el("section", { class: "panel empty", text: "Project details could not be loaded." }));
    refresh.disabled = false;
    return;
  }

  refresh.disabled = false;

  try {
    const fullDashboard = await fetchJSON(`/api/v1/projects/${encodeURIComponent(projectID)}/dashboard-summary`, 12000);
    if (!currentDetail || currentDetail.projectID !== projectID || selectedRoute().projectID !== projectID) return;
    currentDetail.dashboard = mergeDashboard(currentDetail.dashboard, fullDashboard);
    summary.textContent = currentDetail.dashboard.project.display_name || currentDetail.dashboard.project.id;
    renderProjectDetail(currentDetail.dashboard);
    statusBox.textContent = "";
  } catch (error) {
    if (!currentDetail || currentDetail.projectID !== projectID || selectedRoute().projectID !== projectID) return;
    currentDetail.dashboard = withWarning(currentDetail.dashboard, "dashboard_summary_unavailable");
    statusBox.textContent = error.message;
    renderHead();
  } finally {
    refresh.disabled = false;
  }
}

async function loadProjectDetailSkeleton(projectID) {
  const encoded = encodeURIComponent(projectID);
  const [projectsResult, healthResult, latestResult] = await Promise.allSettled([
    loadProjectListForDetail(),
    fetchJSON(`/api/v1/projects/${encoded}/context-health`, 4000),
    fetchJSON(`/api/v1/projects/${encoded}/ingestion-runs/latest`, 4000),
  ]);
  const projects = projectsResult.status === "fulfilled" ? projectsResult.value : [];
  const project = findProject(projects, projectID);
  if (projectsResult.status === "fulfilled" && !project) {
    throw new Error(`/api/v1/projects/${encoded} returned 404`);
  }
  const health = healthResult.status === "fulfilled" ? healthResult.value : null;
  const latest = latestResult.status === "fulfilled" && latestResult.value?.status ? latestResult.value : null;
  const warnings = [];
  if (projectsResult.status !== "fulfilled") warnings.push("project_metadata_unavailable");
  if (healthResult.status !== "fulfilled") warnings.push("context_health_unavailable");
  if (latestResult.status !== "fulfilled") warnings.push("latest_ingestion_unavailable");
  return {
    project: project || fallbackProject(projectID),
    context_health: health,
    latest_run: latest,
    graph: {},
    workspace: null,
    integrations: null,
    warnings,
  };
}

async function loadProjectListForDetail() {
  if (Array.isArray(cachedProjects)) return cachedProjects;
  const data = await fetchJSON("/api/v1/projects", 4000);
  cachedProjects = Array.isArray(data.projects) ? data.projects : [];
  return cachedProjects;
}

function findProject(projects, projectID) {
  if (!Array.isArray(projects)) return null;
  return projects.find((project) => project.id === projectID || (project.aliases || []).includes(projectID)) || null;
}

function fallbackProject(projectID) {
  return {
    id: projectID,
    display_name: projectID,
    description: "",
    aliases: [],
    enabled: false,
    validation_status: "unknown",
    classification: "unknown",
    workspace_mode: "unknown",
    digest_mode: "unknown",
    update_policy: "unknown",
    graph_storage: "unknown",
  };
}

function mergeDashboard(current, next) {
  if (!next || typeof next !== "object") return current;
  return {
    ...current,
    ...next,
    project: next.project || current.project,
    context_health: next.context_health || current.context_health,
    latest_run: next.latest_run || current.latest_run,
    graph: next.graph || current.graph || {},
    workspace: next.workspace ?? current.workspace,
    integrations: next.integrations ?? current.integrations,
    warnings: mergeDashboardWarnings(current, next),
  };
}

function withWarning(dashboard, warning) {
  return {
    ...dashboard,
    warnings: mergeWarnings(dashboard.warnings, [warning]),
  };
}

function mergeWarnings(first, second) {
  return [...new Set([...(Array.isArray(first) ? first : []), ...(Array.isArray(second) ? second : [])])];
}

function mergeDashboardWarnings(current, next) {
  const warnings = Array.isArray(next.warnings) ? next.warnings.slice() : [];
  if (!next.context_health && current.warnings?.includes("context_health_unavailable")) {
    warnings.push("context_health_unavailable");
  }
  if (!next.latest_run && current.warnings?.includes("latest_ingestion_unavailable")) {
    warnings.push("latest_ingestion_unavailable");
  }
  return [...new Set(warnings)];
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
        el("span", { class: "eyebrow", text: "Trace" }),
        el("h2", { class: "activity-drawer__title", text: "Agent activity" }),
      ),
      el("button", { class: "secondary compact", type: "button", onClick: closeActivityDrawer, title: "Close activity drawer" }, "Close"),
    ),
    el("p", { class: "activity-drawer__note", text: "Persisted replay is redacted by default. Trace IDs connect MCP calls, agent runs, workspace edits, verifier metadata, ingestion runs, and promotion decisions." }),
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
  ACTIVITY_EVENT_TYPES.forEach((type) => activitySource.addEventListener(type, handleActivityEvent));
  activitySource.onmessage = handleActivityEvent;
  activitySource.addEventListener("error", () => {
    if (activityStatus) activityStatus.textContent = "Stream disconnected or unavailable";
  });
}

function handleActivityEvent(event) {
    try {
      activityEvents.push(JSON.parse(event.data));
      if (activityEvents.length > 200) activityEvents = activityEvents.slice(-200);
      renderActivityEvents(activityEvents);
    } catch {
      if (activityStatus) activityStatus.textContent = "Received malformed activity event";
    }
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
    activityList.append(emptyText("No agent activity captured for this project yet."));
    return;
  }
  visibleActivityRows(events).forEach((event) => activityList.append(activityEventRow(event)));
}

function visibleActivityRows(events) {
  const rows = [];
  const policyGroups = new Map();
  const reversed = events.slice().reverse();
  for (const event of reversed) {
    if (!isPolicyEvent(event)) {
      rows.push(event);
      continue;
    }
    const key = policyEventKey(event);
    const group = policyGroups.get(key);
    if (group) {
      group._policy_group_count += 1;
      group._policy_group_ids.push(event.id);
      if (!group.relative_path && event.relative_path) group.relative_path = event.relative_path;
      continue;
    }
    const grouped = {
      ...event,
      _policy_group_key: key,
      _policy_group_count: 1,
      _policy_group_ids: [event.id],
    };
    policyGroups.set(key, grouped);
    rows.push(grouped);
  }
  return rows;
}

function isPolicyEvent(event) {
  return event?.event_kind === "policy_event";
}

function policyEventKey(event) {
  return [
    event.project_id || "",
    event.policy_category || event.tool_name || "",
    event.relative_path || "",
  ].join("\u0000");
}

function activityEventRow(event) {
  const statusTone = event.status === "ok" || event.status === "completed" || event.status === "validated" || event.status === "promoted" ? "ok" : "warn";
  const title = event.tool_name || event.method || event.event_kind || "activity";
  const groupedCount = event._policy_group_count || 1;
  const subtitle = groupedCount > 1
    ? `${formatDate(event.timestamp)} - ${groupedCount} repeated policy events`
    : formatDate(event.timestamp);
  const badges = [
    pill(event.status || "unknown", statusTone),
    event.event_kind ? pill(event.event_kind, event.event_kind === "mcp_activity" ? "muted" : "ok") : null,
    groupedCount > 1 ? el("span", { class: "tag", text: `${groupedCount}x` }) : null,
    event.trace_id ? el("span", { class: "tag", text: `trace ${shortID(event.trace_id)}` }) : null,
    event.run_id ? el("span", { class: "tag", text: `run ${shortID(event.run_id)}` }) : null,
    el("span", { class: "tag", text: `${numberValue(event.duration_ms)} ms` }),
  ].filter(Boolean);
  return el("article", { class: "activity-event" },
    el("div", { class: "activity-event__summary" },
      el("div", { class: "activity-event__main" },
        el("strong", { text: title }),
        el("span", { class: "muted-text", text: subtitle }),
      ),
      el("div", { class: "activity-event__badges" }, badges),
    ),
    event.error ? el("p", { class: "activity-event__error", text: event.error }) : null,
    contextPackManifestBlock(event),
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
        event_kind: event.event_kind,
        request_id: event.request_id,
        project_id: event.project_id,
        trace_id: event.trace_id,
        run_id: event.run_id,
        parent_id: event.parent_id,
        correlation_kind: event.correlation_kind,
        method: event.method,
        tool_name: event.tool_name,
        policy_category: event.policy_category,
        relative_path: event.relative_path,
        grouped_event_count: event._policy_group_count,
        grouped_event_ids: event._policy_group_ids,
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

function contextPackManifestBlock(event) {
  const manifest = contextPackManifest(event);
  if (!manifest) return null;
  const counts = [
    ["Files", manifest.file_ids?.length || 0],
    ["Symbols", manifest.symbol_ids?.length || 0],
    ["Chunks", manifest.chunk_ids?.length || 0],
  ];
  return el("div", { class: "activity-manifest" },
    el("div", { class: "activity-manifest__head" },
      el("strong", { text: "Context-pack manifest" }),
      el("span", { class: "tag", text: manifest.graph_status || "unknown" }),
      manifest.contains_source ? el("span", { class: "tag tag--warn", text: "source included" }) : el("span", { class: "tag", text: "manifest only" }),
    ),
    el("div", { class: "activity-manifest__grid" },
      ...counts.map(([label, value]) => el("span", {}, `${label}: ${value}`)),
      manifest.generated_at ? el("span", {}, `Generated: ${formatDate(manifest.generated_at)}`) : null,
      manifest.export_mode ? el("span", {}, `Export: ${manifest.export_mode}`) : null,
    ),
    manifestHashStrip(manifest.redacted_hashes || []),
    el("details", { class: "activity-manifest__details" },
      el("summary", { text: "Manifest details" }),
      el("pre", { text: prettyJSON(manifest) }),
    ),
  );
}

function manifestHashStrip(hashes) {
  if (!hashes.length) return null;
  return el("div", { class: "activity-manifest__hashes" },
    hashes.slice(0, 4).map((hash) => el("span", { class: "tag", title: hash.kind || "hash", text: `${hash.kind || "hash"} ${shortID(hash.value || "")}` })),
  );
}

function contextPackManifest(event) {
  if (event?.tool_name !== "projects.context_pack.build" && event?.tool_name !== "projects_context_pack_build") return null;
  return event?.raw_result?.structuredContent?.manifest || event?.raw_result?.manifest || null;
}

function activityPayloadBlock(title, payload) {
  return el("div", { class: "activity-payload" },
    el("strong", { text: title }),
    el("pre", { text: prettyJSON(payload) }),
  );
}

function copyVisibleActivity() {
  if (!activityEvents.length || !navigator.clipboard) return;
  const jsonl = activityEvents.map((event) => JSON.stringify(redactedActivityEvent(event))).join("\n");
  navigator.clipboard.writeText(jsonl).then(() => {
    if (activityStatus) activityStatus.textContent = "Copied redacted visible activity as JSONL";
  }).catch(() => {
    if (activityStatus) activityStatus.textContent = "Copy failed";
  });
}

function redactedActivityEvent(event) {
  return {
    id: event.id,
    timestamp: event.timestamp,
    request_id: event.request_id,
    project_id: event.project_id,
    event_kind: event.event_kind,
    trace_id: event.trace_id,
    run_id: event.run_id,
    parent_id: event.parent_id,
    correlation_kind: event.correlation_kind,
    method: event.method,
    tool_name: event.tool_name,
    status: event.status,
    duration_ms: event.duration_ms,
    error: event.error,
    failure_category: event.failure_category,
    client_class: event.client_class,
    input_summary_class: event.input_summary_class,
    output_summary_class: event.output_summary_class,
  };
}

function shortID(value) {
  if (!value) return "";
  return value.length > 14 ? `${value.slice(0, 10)}...` : value;
}

function buildTabs(project, health, latest, graph, dashboard) {
  const renderers = {
    overview: () => tabOverview(project, health, latest, graph),
    "work-plan": () => tabWorkPlan(project.id),
    workflows: () => tabProjectWorkflows(project.id),
    graph: () => tabGraph(graph),
    evidence: () => tabEvidenceGraph(project.id),
    confidence: () => tabConfidence(project.id),
    knowledge: () => tabKnowledgePromotion(project.id),
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

function tabWorkPlan(projectID) {
  const planNode = el("div", { class: "work-plan-current" }, emptyText("Loading current work plan..."));
  const planListNode = el("div", { class: "rows" }, emptyText("Loading all work plans..."));
  const planTasksNode = el("div", { class: "work-task-detail" }, emptyText("Select a work plan to inspect task decomposition."));
  const nextNode = el("div", { class: "work-task-detail" }, emptyText("Loading next safe task..."));
  const openNode = el("div", { class: "rows" }, emptyText("Loading open tasks..."));
  const mineNode = el("div", { class: "rows" }, emptyText("Loading open mine..."));
  const blockedNode = el("div", { class: "rows" }, emptyText("Loading blocked tasks..."));
  const automationNode = el("div", { class: "rows" }, emptyText("Loading automation runs..."));
  const graphNode = el("div", { class: "work-task-graph" }, emptyText("Loading task graph..."));
  const detailNode = el("div", { class: "work-task-detail" }, emptyText("Select a work task to inspect safe metadata."));
  const state = { plans: [], planTasks: [], openTasks: [], mineTasks: [], blockedTasks: [], automationRuns: [], selectedPlanID: "", selectedTaskID: "" };
  const body = el("div", { class: "work-plan-view" },
    el("div", { class: "work-plan-layout" },
      block("Current Work Plan", planNode),
      block("Next Safe Task", nextNode),
    ),
    el("div", { class: "work-plan-layout work-plan-layout--wide" },
      block("All Work Plans", planListNode),
      block("Selected Plan Tasks", planTasksNode),
    ),
    el("div", { class: "work-plan-columns work-plan-columns--three" },
      block("Open Tasks", openNode),
      block("Open Mine", mineNode),
      block("Blocked", blockedNode),
    ),
    el("div", { class: "work-plan-layout" },
      block("Automation Runs", automationNode),
      block("Task Detail", detailNode),
    ),
    el("div", { class: "work-plan-layout work-plan-layout--wide" },
      block("Task Graph", graphNode),
      block("Task Links", el("div", { class: "work-task-detail" }, emptyText("Select a task to inspect linked evidence, claims, verification, knowledge, and automation runs."))),
    ),
  );
  body.dataset.projectSubview = "work-plan";
  loadWorkPlanView(projectID, state, { planNode, planListNode, planTasksNode, nextNode, openNode, mineNode, blockedNode, automationNode, graphNode, detailNode });
  return panel("Work Plan", body);
}

function tabProjectWorkflows(projectID) {
  const listNode = el("div", { class: "workflow-list rows" }, emptyText("Loading workflows..."));
  const detailNode = el("div", { class: "workflow-detail" }, emptyText("Select a workflow to inspect governed execution metadata."));
  const state = { workflows: [], selectedWorkflowID: "", validatedWorkflowID: "", lastValidation: null, compiling: false };
  const body = el("div", { class: "workflow-view" },
    el("div", { class: "workflow-layout" },
      block("Workflow List", listNode),
      block("Workflow Detail", detailNode),
    ),
  );
  body.dataset.projectSubview = "workflows";
  loadProjectWorkflows(projectID, state, listNode, detailNode);
  return panel("Project Workflows", body);
}

async function loadProjectWorkflows(projectID, state, listNode, detailNode) {
  try {
    const data = await fetchJSON(`/api/v1/projects/${encodeURIComponent(projectID)}/workflows`, 6000);
    state.workflows = workArray(data, "workflows");
    listNode.replaceChildren(workflowRows(projectID, state, listNode, detailNode));
    if (state.workflows.length) {
      const selected = state.workflows.find((workflow) => workflowID(workflow) === state.selectedWorkflowID) || state.workflows[0];
      if (selected) loadProjectWorkflowDetail(projectID, workflowID(selected), state, listNode, detailNode);
    } else {
      detailNode.replaceChildren(emptyText("No project workflows returned."));
    }
  } catch (error) {
    listNode.replaceChildren(emptyText(`Workflows unavailable: ${error.message}`));
    detailNode.replaceChildren(emptyText("Workflow detail unavailable."));
  }
}

function workflowRows(projectID, state, listNode, detailNode) {
  if (!state.workflows.length) return emptyText("No workflows returned.");
  return el("div", { class: "workflow-list rows" }, state.workflows.map((workflow) => workflowButton(projectID, workflow, state, listNode, detailNode)));
}

function workflowButton(projectID, workflow, state, listNode, detailNode) {
  const id = workflowID(workflow);
  const selected = id && id === state.selectedWorkflowID;
  return el("button", {
    class: `workflow-row${selected ? " workflow-row--selected" : ""}`,
    type: "button",
    disabled: !id,
    onClick: () => loadProjectWorkflowDetail(projectID, id, state, listNode, detailNode),
  },
    el("div", { class: "workflow-row__head" },
      el("strong", { text: safeWorkText(workflow.title || workflow.workflow_ref || id) || "workflow" }),
      pill(safeWorkText(workflow.status) || "status unknown", workflowTone(workflow.status)),
    ),
    el("div", { class: "workflow-row__stats" },
      workflowStat("Agents", workflowAgents(workflow).length),
      workflowStat("Steps", workflowSteps(workflow).length),
      workflowStat("Review Gates", workflowReviewGates(workflow).length),
    ),
    el("span", { text: compactJoin([workflow.workflow_ref || id, `updated ${formatDate(workflow.updated_at || workflow.created_at)}`]) }),
  );
}

function workflowStat(label, value) {
  return el("span", { class: "workflow-stat" }, el("strong", { text: numberValue(value) }), el("span", { text: label }));
}

async function loadProjectWorkflowDetail(projectID, workflowIDValue, state, listNode, detailNode) {
  if (!workflowIDValue) return;
  state.selectedWorkflowID = workflowIDValue;
  state.validatedWorkflowID = state.validatedWorkflowID === workflowIDValue ? state.validatedWorkflowID : "";
  state.lastValidation = state.validatedWorkflowID ? state.lastValidation : null;
  detailNode.replaceChildren(emptyText("Loading workflow metadata..."));
  listNode.replaceChildren(workflowRows(projectID, state, listNode, detailNode));
  const encodedProject = encodeURIComponent(projectID);
  const encodedWorkflow = encodeURIComponent(workflowIDValue);
  const [workflowResult, agentsResult, snapshotsResult] = await Promise.allSettled([
    fetchJSON(`/api/v1/projects/${encodedProject}/workflows/${encodedWorkflow}`, 6000),
    fetchJSON(`/api/v1/projects/${encodedProject}/workflows/${encodedWorkflow}/agent-definitions`, 6000),
    fetchJSON(`/api/v1/projects/${encodedProject}/permission-snapshots?workflow_id=${encodedWorkflow}`, 6000),
  ]);
  if (state.selectedWorkflowID !== workflowIDValue) return;
  if (workflowResult.status !== "fulfilled") {
    detailNode.replaceChildren(emptyText(`Workflow unavailable: ${workflowResult.reason?.message || "request failed"}`));
    return;
  }
  const workflow = workflowResult.value || {};
  const agents = agentsResult.status === "fulfilled" ? workArray(agentsResult.value, "agent_definitions", "agents") : workflowAgents(workflow);
  const snapshots = snapshotsResult.status === "fulfilled" ? workArray(snapshotsResult.value, "permission_snapshots") : workflowPermissionSnapshots(workflow);
  detailNode.replaceChildren(workflowDetail(projectID, workflow, agents, snapshots, state, listNode, detailNode));
}

function workflowDetail(projectID, workflow, agents, snapshots, state, listNode, detailNode) {
  const id = workflowID(workflow);
  return el("div", { class: "workflow-detail__body" },
    infoList([
      ["Workflow", id || "unknown"],
      ["Ref", safeWorkText(workflow.workflow_ref) || "unknown"],
      ["Status", safeWorkText(workflow.status) || "unknown"],
      ["Agents", numberValue(agents.length)],
      ["Steps", numberValue(workflowSteps(workflow).length)],
      ["Review Gates", numberValue(workflowReviewGates(workflow).length)],
      ["Updated", formatDate(workflow.updated_at || workflow.created_at)],
    ]),
    workTextBlock("Purpose", workflow.purpose),
    block("Agents", workflowAgentList(agents)),
    block("Permission Summary", workflowPermissionSummary(snapshots, agents)),
    block("Steps And Dependencies", workflowStepList(workflowSteps(workflow))),
    block("Review Gates", workflowReviewGateList(workflowReviewGates(workflow))),
    block("Compile Action", workflowCompileAction(projectID, id, state, listNode, detailNode)),
  );
}

function workflowAgentList(agents) {
  if (!agents.length) return emptyText("No agent definitions returned.");
  return el("div", { class: "workflow-grid" }, agents.map((agent) => el("div", { class: "tile" },
    el("div", { class: "tile__head" },
      el("strong", { text: safeWorkText(agent.display_name || agent.id) || "agent" }),
      pill(safeWorkText(agent.workspace_mode) || "workspace unknown", "muted"),
    ),
    el("span", { class: "tile__sub", text: safeWorkSummary(agent.purpose) || "No purpose returned." }),
    el("div", { class: "tag-list" }, [
      ...workflowSafeRefs(agent.allowed_skills).map((value) => el("span", { class: "tag", text: `skill ${value}` })),
      ...workflowSafeRefs(agent.allowed_tools).map((value) => el("span", { class: "tag", text: `tool ${value}` })),
    ]),
    infoList([
      ["Network", safeWorkText(agent.network_policy) || "unknown"],
      ["Secrets", safeWorkText(agent.secret_policy) || "unknown"],
      ["Logs", safeWorkText(agent.log_policy) || "unknown"],
      ["Max retries", numberValue(agent.max_retries || 0)],
    ]),
  )));
}

function workflowPermissionSummary(snapshots, agents) {
  const records = snapshots.length ? snapshots : agents;
  if (!records.length) return emptyText("No permission snapshots returned.");
  return el("div", { class: "workflow-permissions" }, records.map((record) => el("div", { class: "workflow-permission" },
    el("div", { class: "workflow-row__head" },
      el("strong", { text: safeWorkText(record.agent_id || record.id) || "agent" }),
      pill(safeWorkText(record.workspace_mode) || "workspace unknown", "muted"),
    ),
    infoList([
      ["Allowed skills", numberValue(workflowSafeRefs(record.allowed_skills).length)],
      ["Allowed tools", numberValue(workflowSafeRefs(record.allowed_tools).length)],
      ["Allowed commands", numberValue(workflowSafeRefs(record.allowed_commands).length)],
      ["Denied commands", numberValue(workflowSafeRefs(record.denied_commands).length)],
      ["Network", safeWorkText(record.network_policy) || "unknown"],
      ["Secrets", safeWorkText(record.secret_policy) || "unknown"],
      ["Logs", safeWorkText(record.log_policy) || "unknown"],
    ]),
  )));
}

function workflowStepList(steps) {
  if (!steps.length) return emptyText("No workflow steps returned.");
  return el("div", { class: "workflow-step-list" }, steps.map((step) => el("section", { class: "workflow-step" },
    el("div", { class: "workflow-row__head" },
      el("strong", { text: safeWorkText(step.title || step.id) || "step" }),
      pill(safeWorkText(step.kind) || "kind unknown", "muted"),
    ),
    el("span", { class: "work-note", text: compactJoin([step.id, step.agent ? `agent ${step.agent}` : "", step.verification_requirement]) }),
    workRefsBlock("Dependencies", workflowSafeRefs(step.depends_on)),
    workTextBlock("Description", step.description),
    workRefsBlock("Evidence Needed", workflowSafeRefs(step.evidence_needed)),
    workRefsBlock("Context Pack Refs", workflowSafeRefs(step.context_pack_refs)),
    workRefsBlock("Likely Files Affected", safeChangedPaths(step.likely_files_affected)),
    workTextBlock("Expected Output", step.expected_output),
    workTextBlock("Failure Criteria", step.failure_criteria),
    workTextBlock("Resume Instructions", step.resume_instructions),
  )));
}

function workflowReviewGateList(gates) {
  if (!gates.length) return emptyText("No review gates returned.");
  return el("div", { class: "workflow-step-list" }, gates.map((gate) => el("section", { class: "workflow-step workflow-step--gate" },
    el("div", { class: "workflow-row__head" },
      el("strong", { text: safeWorkText(gate.id) || "review gate" }),
      pill(gate.required ? "required" : "optional", gate.required ? "warn" : "muted"),
    ),
    infoList([
      ["Reviewer", safeWorkText(gate.reviewer_agent) || "unknown"],
      ["Independent", gate.independent_from_owner ? "yes" : "no"],
      ["Applies to", workflowSafeRefs(gate.applies_to).join(", ") || "unspecified"],
      ["Allowed actions", workflowSafeRefs(gate.allowed_actions).join(", ") || "none"],
    ]),
    workRefsBlock("Required Artifacts", workflowSafeRefs(gate.required_artifacts)),
    el("details", { class: "workflow-instructions" },
      el("summary", { text: "Reviewer Instructions" }),
      el("p", { class: "work-note", text: safeWorkSummary(gate.instructions) || "No reviewer instructions returned." }),
    ),
  )));
}

function workflowCompileAction(projectID, workflowIDValue, state, listNode, detailNode) {
  const status = el("div", { class: "workflow-compile__status" },
    state.lastValidation ? workflowCompileResult(state.lastValidation) : emptyText("Validate compile first. Compilation requires a second explicit action after validation passes."),
  );
  if (state.compileNotice) status.append(emptyText(state.compileNotice));
  const validateButton = el("button", {
    type: "button",
    class: "compact",
    disabled: state.compiling || !workflowIDValue,
    onClick: () => validateWorkflowCompile(projectID, workflowIDValue, state, status, listNode, detailNode),
    text: "Validate compile",
  });
  const children = [el("div", { class: "workflow-compile__actions" }, validateButton)];
  if (state.validatedWorkflowID === workflowIDValue && !workflowCompileHasErrors(state.lastValidation)) {
    children[0].append(el("button", {
      type: "button",
      class: "compact",
      disabled: state.compiling,
      onClick: () => compileValidatedWorkflow(projectID, workflowIDValue, state, status, listNode, detailNode),
      text: "Compile validated workflow",
    }));
  }
  children.push(status);
  return el("div", { class: "workflow-compile" }, ...children);
}

async function validateWorkflowCompile(projectID, workflowIDValue, state, status, listNode, detailNode) {
  state.compiling = true;
  state.validatedWorkflowID = "";
  state.lastValidation = null;
  state.compileNotice = "";
  status.replaceChildren(emptyText("Validating workflow compile..."));
  try {
    const result = await postWorkflowCompile(projectID, workflowIDValue, { dry_run: true });
    state.lastValidation = result;
    if (!workflowCompileHasErrors(result)) state.validatedWorkflowID = workflowIDValue;
    state.compileNotice = "Validation complete. Use the second explicit action to compile.";
    state.compiling = false;
    const root = status.closest(".workflow-compile");
    if (root) root.replaceWith(workflowCompileAction(projectID, workflowIDValue, state, listNode, detailNode));
  } catch (error) {
    status.replaceChildren(emptyText(error.message));
  } finally {
    state.compiling = false;
  }
}

async function compileValidatedWorkflow(projectID, workflowIDValue, state, status, listNode, detailNode) {
  if (state.validatedWorkflowID !== workflowIDValue || workflowCompileHasErrors(state.lastValidation)) {
    status.replaceChildren(emptyText("Compile blocked until validation passes."));
    return;
  }
  state.compiling = true;
  status.replaceChildren(emptyText("Compiling validated workflow..."));
  try {
    const result = await postWorkflowCompile(projectID, workflowIDValue, { dry_run: false });
    state.lastValidation = result;
    state.validatedWorkflowID = "";
    status.replaceChildren(workflowCompileResult(result));
    loadProjectWorkflowDetail(projectID, workflowIDValue, state, listNode, detailNode);
  } catch (error) {
    status.replaceChildren(emptyText(error.message));
  } finally {
    state.compiling = false;
  }
}

async function postWorkflowCompile(projectID, workflowIDValue, payload) {
  return fetchJSONWithOptions(`/api/v1/projects/${encodeURIComponent(projectID)}/workflows/${encodeURIComponent(workflowIDValue)}/compile`, 10000, {
    method: "POST",
    headers: { "Accept": "application/json", "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
}

function workflowCompileResult(result) {
  const issues = workflowIssues(result);
  return el("div", { class: "workflow-compile-result" },
    infoList([
      ["Workflow", safeWorkText(result?.workflow_id) || "unknown"],
      ["Dry run", result?.dry_run ? "yes" : "no"],
      ["Work plan", safeWorkText(result?.work_plan_id) || "none"],
      ["Work tasks", numberValue(workflowSafeRefs(result?.work_task_ids).length)],
      ["Review tasks", numberValue(workflowSafeRefs(result?.reviewer_task_ids).length)],
      ["Automations", numberValue(workflowSafeRefs(result?.automation_ids).length)],
      ["Compiled", formatDate(result?.compiled_at)],
    ]),
    issues.length ? workflowIssueList(issues) : emptyText("No validation issues returned."),
  );
}

function workflowIssueList(issues) {
  return el("div", { class: "rows" }, issues.map((issue) => el("div", { class: "row row--file" },
    el("strong", { text: safeWorkText(issue.code) || "validation_issue" }),
    el("span", { text: compactJoin([issue.severity, issue.field_path]) }),
    el("span", { text: safeWorkSummary(issue.message) }),
  )));
}

function workflowCompileHasErrors(result) {
  return workflowIssues(result).some((issue) => safeWorkText(issue.severity).toLowerCase() === "error");
}

function workflowIssues(result) {
  return workArray(result, "validation_issues", "issues");
}

function workflowID(workflow) {
  return safeWorkText(workflow?.id || workflow?.workflow_id);
}

function workflowAgents(workflow) {
  return workArray(workflow, "agents", "agent_definitions");
}

function workflowSteps(workflow) {
  return workArray(workflow, "steps");
}

function workflowReviewGates(workflow) {
  return workArray(workflow, "review_gates");
}

function workflowPermissionSnapshots(workflow) {
  return workArray(workflow, "permission_snapshots");
}

function workflowTone(status) {
  const value = safeWorkText(status).toLowerCase();
  if (["enabled", "active"].includes(value)) return "ok";
  if (["disabled", "superseded"].includes(value)) return "muted";
  return "warn";
}

function workflowSafeRefs(value) {
  return workRefs(value);
}

async function loadWorkPlanView(projectID, state, nodes) {
  const encoded = encodeURIComponent(projectID);
  const [plans, next, open, mine, blocked, automation] = await Promise.allSettled([
    fetchJSON(`/api/v1/projects/${encoded}/work-plans?page_size=10`, 6000),
    fetchJSON(`/api/v1/projects/${encoded}/work-tasks/next`, 6000),
    fetchJSON(`/api/v1/projects/${encoded}/work-tasks/open?page_size=24`, 6000),
    fetchJSON(`/api/v1/projects/${encoded}/work-tasks/mine?page_size=12`, 6000),
    fetchJSON(`/api/v1/projects/${encoded}/work-tasks/blocked?page_size=12`, 6000),
    fetchJSON(`/api/v1/projects/${encoded}/automation-runs`, 6000),
  ]);

  const planRecords = plans.status === "fulfilled" ? workArray(plans.value, "work_plans", "plans") : [];
  const nextTask = next.status === "fulfilled" ? workTaskRecord(next.value) : null;
  const openTasks = open.status === "fulfilled" ? workArray(open.value, "work_tasks", "tasks") : [];
  const mineTasks = mine.status === "fulfilled" ? workArray(mine.value, "work_tasks", "tasks") : [];
  const blockedTasks = blocked.status === "fulfilled" ? workArray(blocked.value, "work_tasks", "tasks") : [];
  const automationRuns = automation.status === "fulfilled" ? workArray(automation.value, "automation_runs", "runs") : [];

  state.plans = planRecords;
  state.openTasks = openTasks;
  state.mineTasks = mineTasks;
  state.blockedTasks = blockedTasks;
  state.automationRuns = automationRuns;
  if (planRecords.length && !planRecords.some((plan) => workPlanID(plan) === state.selectedPlanID)) {
    const current = planRecords.find((plan) => ["active", "needs_review"].includes(workStatus(plan))) || planRecords[0];
    state.selectedPlanID = workPlanID(current);
  }

  nodes.planNode.replaceChildren(
    plans.status === "fulfilled"
      ? workPlanCurrent(planRecords, nextTask)
      : emptyText(`Work plans unavailable: ${plans.reason?.message || "request failed"}`),
  );
  nodes.planListNode.replaceChildren(
    plans.status === "fulfilled"
      ? workPlanRows(projectID, planRecords, state, nodes.planTasksNode, nodes.detailNode)
      : emptyText(`Work plan history unavailable: ${plans.reason?.message || "request failed"}`),
  );
  nodes.nextNode.replaceChildren(
    next.status === "fulfilled"
      ? workTaskDetail(projectID, nextTask, "No safe next task returned.")
      : emptyText(`Next safe task unavailable: ${next.reason?.message || "request failed"}`),
  );
  nodes.openNode.replaceChildren(
    open.status === "fulfilled"
      ? workTaskRows(projectID, openTasks, state, nodes.detailNode)
      : emptyText(`Open tasks unavailable: ${open.reason?.message || "request failed"}`),
  );
  nodes.mineNode.replaceChildren(
    mine.status === "fulfilled"
      ? workTaskRows(projectID, mineTasks, state, nodes.detailNode)
      : emptyText(`Open mine unavailable: ${mine.reason?.message || "request failed"}`),
  );
  nodes.blockedNode.replaceChildren(
    blocked.status === "fulfilled"
      ? workTaskRows(projectID, blockedTasks, state, nodes.detailNode)
      : emptyText(`Blocked tasks unavailable: ${blocked.reason?.message || "request failed"}`),
  );
  nodes.automationNode.replaceChildren(
    automation.status === "fulfilled"
      ? automationRunRows(automationRuns)
      : emptyText(`Automation runs unavailable: ${automation.reason?.message || "request failed"}`),
  );
  nodes.graphNode.replaceChildren(workTaskGraph(projectID, [...openTasks, ...mineTasks, ...blockedTasks], state, nodes.detailNode));
  if (state.selectedPlanID) {
    loadWorkPlanTasks(projectID, state.selectedPlanID, state, nodes.planTasksNode, nodes.detailNode);
  } else {
    nodes.planTasksNode.replaceChildren(emptyText("No work plan selected."));
  }
}

function workPlanCurrent(plans, nextTask) {
  const current = plans.find((plan) => ["active", "in_progress", "current"].includes(workStatus(plan))) || plans[0] || {};
  if (!plans.length && !nextTask) return emptyText("No current work plan metadata returned.");
  return el("div", { class: "work-plan-current__body" },
    infoList([
      ["Plan", safeWorkText(current.plan_ref || current.id || current.work_plan_id) || "unknown"],
      ["Status", safeWorkText(current.status) || "unknown"],
      ["Owner", safeWorkText(current.owner_agent || current.owner) || "unassigned"],
      ["Isolation", safeWorkText(current.isolation_mode) || "unspecified"],
      ["Worktree", safeWorkText(current.git_worktree_ref) || "none"],
      ["Updated", formatDate(current.updated_at || current.last_updated_at)],
    ]),
    workTextBlock("Resume Instructions", current.resume_instructions || nextTask?.resume_instructions),
    workSafeLinks("Safe metadata links", [
      workTabLink("Evidence", "evidence"),
      workTabLink("Confidence", "confidence"),
      workTabLink("Knowledge", "knowledge"),
      workTabLink("Activity", "activity"),
    ]),
  );
}

function workPlanRows(projectID, plans, state, planTasksNode, detailNode) {
  if (!plans.length) return emptyText("No work plan history returned.");
  return el("div", { class: "rows" }, plans.map((plan) => workPlanButton(projectID, plan, state, planTasksNode, detailNode)));
}

function workPlanButton(projectID, plan, state, planTasksNode, detailNode) {
  const planID = workPlanID(plan);
  const selected = planID && planID === state.selectedPlanID;
  return el("button", {
    class: `work-plan-row${selected ? " work-plan-row--selected" : ""}`,
    type: "button",
    disabled: !planID,
    onClick: () => loadWorkPlanTasks(projectID, planID, state, planTasksNode, detailNode),
  },
    el("div", { class: "work-task-row__head" },
      el("strong", { text: safeWorkText(plan.title || plan.plan_ref || planID) || "work plan" }),
      pill(safeWorkText(plan.status) || "status unknown", workTaskTone(plan.status)),
    ),
    el("span", { text: compactJoin([planID, plan.plan_ref, plan.owner_agent || plan.created_by_run_id, plan.isolation_mode, plan.git_worktree_ref, formatDate(plan.updated_at || plan.created_at)]) }),
  );
}

async function loadWorkPlanTasks(projectID, planID, state, planTasksNode, detailNode) {
  state.selectedPlanID = planID;
  planTasksNode.replaceChildren(emptyText("Loading selected plan task decomposition..."));
  try {
    const tasks = await fetchJSON(`/api/v1/projects/${encodeURIComponent(projectID)}/work-tasks?plan_id=${encodeURIComponent(planID)}&page_size=100`, 6000);
    const taskRecords = workArray(tasks, "work_tasks", "tasks");
    state.planTasks = taskRecords;
    planTasksNode.replaceChildren(workPlanTaskDecomposition(projectID, selectedWorkPlan(state), taskRecords, state, detailNode));
  } catch (error) {
    planTasksNode.replaceChildren(emptyText(error.message));
  }
}

function selectedWorkPlan(state) {
  return state.plans.find((plan) => workPlanID(plan) === state.selectedPlanID) || null;
}

function workPlanTaskDecomposition(projectID, plan, tasks, state, detailNode) {
  if (!plan) return emptyText("Selected work plan metadata unavailable.");
  return el("div", { class: "work-task-detail__body" },
    infoList([
      ["Plan", workPlanID(plan) || "unknown"],
      ["Plan Ref", safeWorkText(plan.plan_ref) || "unknown"],
      ["Status", safeWorkText(plan.status) || "unknown"],
      ["Owner", safeWorkText(plan.owner_agent || plan.created_by_run_id) || "unassigned"],
      ["Isolation", safeWorkText(plan.isolation_mode) || "unspecified"],
      ["Parallel group", safeWorkText(plan.parallel_group_ref) || "none"],
      ["Workspace", safeWorkText(plan.workspace_ref) || "none"],
      ["Git base", safeWorkText(plan.git_base_ref) || "none"],
      ["Git branch", safeWorkText(plan.git_branch_ref) || "none"],
      ["Git worktree", safeWorkText(plan.git_worktree_ref) || "none"],
      ["Updated", formatDate(plan.updated_at || plan.created_at)],
    ]),
    workTextBlock("Goal", plan.goal_summary),
    workTextBlock("Resume Summary", plan.resume_summary),
    tasks.length
      ? workTaskGraph(projectID, tasks, state, detailNode)
      : emptyText("No tasks returned for selected work plan."),
  );
}

function workTaskRows(projectID, tasks, state, detailNode) {
  if (!tasks.length) return emptyText("No matching work tasks.");
  return el("div", { class: "rows" }, tasks.map((task) => workTaskButton(projectID, task, state, detailNode)));
}

function workTaskButton(projectID, task, state, detailNode) {
  const taskID = workTaskID(task);
  return el("button", {
    class: "work-task-row",
    type: "button",
    disabled: !taskID,
    onClick: () => loadWorkTaskDetail(projectID, taskID, state, detailNode),
  },
    el("div", { class: "work-task-row__head" },
      el("strong", { text: safeWorkText(task.title || task.task_ref || taskID) || "work task" }),
      pill(safeWorkText(task.status) || "status unknown", workTaskTone(task.status)),
    ),
    el("span", { text: compactJoin([taskID, task.owner_agent || task.claimed_by_run_id, task.verification_requirement]) }),
  );
}

async function loadWorkTaskDetail(projectID, taskID, state, detailNode) {
  state.selectedTaskID = taskID;
  detailNode.replaceChildren(emptyText("Loading work task metadata..."));
  try {
    const task = await fetchJSON(`/api/v1/projects/${encodeURIComponent(projectID)}/work-tasks/${encodeURIComponent(taskID)}`, 6000);
    detailNode.replaceChildren(workTaskDetail(projectID, task, "Work task metadata unavailable."));
  } catch (error) {
    detailNode.replaceChildren(emptyText(error.message));
  }
}

function workTaskDetail(projectID, task, emptyMessage) {
  if (!task) return emptyText(emptyMessage);
  const taskID = workTaskID(task);
  const deps = workRefs(task.dependency_task_ids || task.dependencies_done || task.dependencies || task.blocked_by_task_ids);
  const evidenceNeeded = workRefs(task.evidence_needed);
  const evidence = workRefs(task.evidence_refs);
  const claims = workRefs(task.claim_refs);
  const verifierResults = workRefs(task.verifier_result_refs);
  const contextPacks = workRefs(task.context_pack_refs);
  const files = safeChangedPaths(task.likely_files_affected || task.changed_files || task.files);
  const knowledge = workRefs(task.knowledge_candidate_refs || task.knowledge_candidates);
  const artifacts = workRefs(task.artifact_refs);
  const agentRuns = workRefs(task.agent_run_ids);
  return el("div", { class: "work-task-detail__body" },
    infoList([
      ["Task", taskID || "unknown"],
      ["Status", safeWorkText(task.status) || "unknown"],
      ["Owner agent", safeWorkText(task.owner_agent || task.claimed_by_run_id) || "unassigned"],
      ["Updated", formatDate(task.updated_at || task.last_updated_at)],
    ]),
    workTextBlock("Title", task.title || task.task_title),
    workTextBlock("Why Safe", task.why_safe),
    workTextBlock("Verification Requirement", task.verification_requirement),
    workTextBlock("Resume Instructions", task.resume_instructions),
    workTextBlock("Blocked Reason", task.blocked_reason),
    workTextBlock("Safe Next Action", task.safe_next_action),
    workRefsBlock("Dependencies", deps),
    workRefsBlock("Evidence Needed", evidenceNeeded),
    workRefsBlock("Attached Evidence", evidence),
    workRefsBlock("Attached Claims", claims),
    workRefsBlock("Verifier Results", verifierResults),
    workRefsBlock("Context Pack Refs", contextPacks),
    workRefsBlock("Likely Files Affected", files),
    workRefsBlock("Knowledge Candidates", knowledge),
    workRefsBlock("Artifact Refs", artifacts),
    workRefsBlock("Agent Run Refs", agentRuns),
    workSafeLinks("Safe metadata links", workTaskLinks(projectID, taskID, evidence, knowledge)),
  );
}

function automationRunRows(runs) {
  if (!runs.length) return emptyText("No automation runs returned.");
  return el("div", { class: "work-task-table automation-run-table", role: "table", "aria-label": "Automation run metadata" },
    el("div", { class: "work-task-table__row automation-run-table__row work-task-table__head", role: "row" },
      ["Run Metadata", "Status", "Plan", "Task", "Runner", "Attempt", "Queue Time", "Summary"].map((label) => el("span", { role: "columnheader", text: label })),
    ),
    runs.map((run) => el("div", { class: "work-task-table__row automation-run-table__row", role: "row" },
      [
        automationRunMetadata(run),
        automationRunStatus(run),
        safeWorkText(run.plan_id || run.plan_ref || run.work_plan_id) || "none",
        safeWorkText(run.task_id || run.task_ref || run.work_task_id) || "none",
        safeWorkText(run.runner_kind) || "unknown",
        String(run.attempt_count ?? 0),
        automationRunQueueTime(run),
        safeWorkText(run.safe_summary || run.failure_category) || "none",
      ].map((value) => el("span", { role: "cell", text: value })),
    )),
  );
}

function automationRunMetadata(run) {
  return compactJoin([
    safeWorkText(run.id) || "unknown",
    run.automation_id ? `automation ${run.automation_id}` : "",
    run.orchestrator_run_id ? `orchestrator ${run.orchestrator_run_id}` : "",
    run.parent_run_id ? `parent ${run.parent_run_id}` : "",
  ]);
}

function automationRunStatus(run) {
  return compactJoin([
    safeWorkText(run.status) || "unknown",
    run.work_task_status ? `task ${run.work_task_status}` : "",
  ]);
}

function automationRunQueueTime(run) {
  return compactJoin([
    run.created_at ? `created ${formatDate(run.created_at)}` : "",
    run.updated_at ? `updated ${formatDate(run.updated_at)}` : "",
    run.started_at ? `started ${formatDate(run.started_at)}` : "",
    run.finished_at ? `finished ${formatDate(run.finished_at)}` : "",
  ]) || "unknown";
}

function workTaskGraph(projectID, tasks, state, detailNode) {
  const byID = new Map();
  tasks.forEach((task) => {
    const id = workTaskID(task);
    if (id && !byID.has(id)) byID.set(id, task);
  });
  const unique = Array.from(byID.values());
  if (!unique.length) return emptyText("No task graph metadata returned.");
  return el("div", { class: "work-task-table", role: "table", "aria-label": "Work task graph metadata" },
    el("div", { class: "work-task-table__row work-task-table__head", role: "row" },
      ["Task Ref", "Status", "Owner", "Dependencies", "Evidence", "Verification", "Knowledge Candidates"].map((label) => el("span", { role: "columnheader", text: label })),
    ),
    unique.map((task) => {
      const taskID = workTaskID(task);
      return el("button", {
        class: "work-task-table__row",
        type: "button",
        role: "row",
        disabled: !taskID,
        onClick: () => loadWorkTaskDetail(projectID, taskID, state, detailNode),
      },
        [
          taskID || "unknown",
          safeWorkText(task.status) || "unknown",
          safeWorkText(task.owner_agent || task.claimed_by_run_id) || "unassigned",
          workRefs(task.dependency_task_ids || task.dependencies || task.blocked_by_task_ids).join(", ") || "none",
          workRefs(task.evidence_refs || task.evidence_needed).join(", ") || "none",
          safeWorkText(task.verification_requirement) || "not specified",
          workRefs(task.knowledge_candidate_refs || task.knowledge_candidates).join(", ") || "none",
        ].map((value) => el("span", { role: "cell", text: value })),
      );
    }),
  );
}

function workTaskLinks(projectID, taskID, evidence, knowledge) {
  const links = [
    workTabLink("Evidence", "evidence", evidence[0] ? { claim_id_search: evidence[0] } : null),
    workTabLink("Confidence", "confidence", evidence[0] ? { claim_id_search: evidence[0] } : null),
    workTabLink("Knowledge", "knowledge", knowledge[0] ? { knowledge_ref: knowledge[0] } : null),
    workTabLink("Activity", "activity", taskID ? { task_id: taskID } : null),
  ];
  return links.map((link) => ({ ...link, projectID }));
}

function workTabLink(label, tab, params) {
  return { label, tab, params: params || {} };
}

function workSafeLinks(title, links) {
  return block(title,
    el("div", { class: "work-link-list" }, links.map((link) => {
      const tab = link.tab === "activity" ? selectedRoute().tab : link.tab;
      const href = link.tab === "activity" ? "" : `#project=${encodeURIComponent(link.projectID || selectedRoute().projectID)}&tab=${encodeURIComponent(tab)}`;
      return el("button", {
        class: "secondary compact",
        type: "button",
        onClick: () => {
          if (link.tab === "activity") openActivityDrawer(link.projectID || selectedRoute().projectID);
          else location.hash = href.slice(1);
        },
        text: link.label,
        title: workLinkTitle(link),
      });
    })),
  );
}

function workLinkTitle(link) {
  const params = Object.entries(link.params || {})
    .map(([key, value]) => `${key}: ${safeWorkText(value)}`)
    .filter(Boolean)
    .join(" | ");
  return params ? `${link.label} metadata | ${params}` : `${link.label} metadata`;
}

function workTextBlock(title, value) {
  const text = safeWorkSummary(value);
  return text ? block(title, el("p", { class: "work-note", text })) : null;
}

function workRefsBlock(title, refs) {
  return block(title,
    refs.length
      ? el("div", { class: "tag-list" }, refs.map((ref) => el("span", { class: "tag", text: ref })))
      : emptyText("No metadata refs returned."),
  );
}

function workArray(value, ...keys) {
  if (Array.isArray(value)) return value;
  for (const key of keys) {
    if (Array.isArray(value?.[key])) return value[key];
  }
  return [];
}

function workTaskRecord(value) {
  if (!value || typeof value !== "object") return null;
  return value.task || value.work_task || value.next_task || value;
}

function workTaskID(task) {
  return safeWorkText(task?.task_ref || task?.id || task?.work_task_id || task?.task_id);
}

function workPlanID(plan) {
  return safeWorkText(plan?.id || plan?.work_plan_id || plan?.plan_id);
}

function workStatus(task) {
  return safeWorkText(task?.status).toLowerCase();
}

function workTaskTone(status) {
  const value = safeWorkText(status).toLowerCase();
  if (["blocked", "failed"].includes(value)) return "warn";
  if (["completed", "done", "verified"].includes(value)) return "ok";
  return "muted";
}

function workRefs(value) {
  const values = Array.isArray(value) ? value : value ? [value] : [];
  return values.map((item) => {
    if (typeof item === "string") return safeWorkText(item);
    return safeWorkText(item.ref || item.id || item.claim_id || item.evidence_ref || item.knowledge_ref || item.task_ref);
  }).filter(Boolean);
}

function safeWorkText(value) {
  return safeEvidenceText(value);
}

function safeWorkSummary(value) {
  return safeEvidenceSummary(value);
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

function tabEvidenceGraph(projectID) {
  const listNode = el("div", { class: "rows" }, emptyText("Loading evidence claims..."));
  const detailNode = el("div", { class: "evidence-detail" }, emptyText("Select a claim to inspect the chain."));
  const pagerNode = el("div", { class: "integration-pager" });
  const state = { claims: [], nextPageToken: "", loading: false, selectedClaimID: "" };
  const controls = evidenceFilterForm(projectID, state, listNode, pagerNode, detailNode);
  const body = el("div", { class: "evidence-layout" },
    block("Claims", controls, listNode, pagerNode),
    block("Claim chain", detailNode),
  );
  loadEvidenceClaims(projectID, state, listNode, pagerNode, detailNode, false);
  return panel("Evidence Graph", body);
}

function evidenceFilterForm(projectID, state, listNode, pagerNode, detailNode) {
  const claimSearch = evidenceInput("Claim id", "claim_id_search");
  const artifactRef = evidenceInput("Artifact ref", "artifact_ref");
  const runID = evidenceInput("Run id", "run_id");
  const traceID = evidenceInput("Trace id", "trace_id");
  const promotionState = evidenceSelect("Promotion", "promotion_state", ["", "candidate", "validated", "promoted", "rejected"]);
  const outcomeStatus = evidenceSelect("Outcome", "outcome_status", ["", "passed", "failed", "blocked", "unknown"]);
  const fields = { claimSearch, artifactRef, runID, traceID, promotionState, outcomeStatus };
  const form = el("form", {
    class: "evidence-filters",
    onSubmit: async (event) => {
      event.preventDefault();
      await loadEvidenceClaims(projectID, state, listNode, pagerNode, detailNode, false, fields);
    },
  },
    claimSearch,
    artifactRef,
    promotionState,
    outcomeStatus,
    runID,
    traceID,
    el("button", { type: "submit", class: "compact", text: "Apply" }),
    el("button", {
      type: "button",
      class: "secondary compact",
      onClick: () => {
        Object.values(fields).forEach((field) => { field.value = ""; });
        loadEvidenceClaims(projectID, state, listNode, pagerNode, detailNode, false, fields);
      },
      text: "Clear",
    }),
  );
  form.dataset.projectSubview = "evidence-graph";
  return form;
}

function evidenceInput(label, name) {
  return el("input", { type: "search", name, placeholder: label, "aria-label": label, autocomplete: "off" });
}

function evidenceSelect(label, name, values) {
  return el("select", { name, "aria-label": label },
    values.map((value) => el("option", { value, text: value || label })),
  );
}

async function loadEvidenceClaims(projectID, state, listNode, pagerNode, detailNode, append, fields) {
  if (state.loading) return;
  state.loading = true;
  renderEvidencePager(projectID, state, listNode, pagerNode, detailNode, fields);
  const filter = evidenceFilterValues(fields);
  if (!append) {
    state.nextPageToken = "";
    state.selectedClaimID = "";
    listNode.replaceChildren(emptyText("Loading evidence claims..."));
    detailNode.replaceChildren(emptyText("Select a claim to inspect the chain."));
  }
  try {
    if (!append && filter.claimSearch) {
      await loadEvidenceClaimByID(projectID, filter.claimSearch, state, listNode, detailNode);
      return;
    }
    const params = new URLSearchParams({ page_size: "20" });
    if (append && state.nextPageToken) params.set("page_token", state.nextPageToken);
    setParam(params, "artifact_ref", filter.artifactRef);
    setParam(params, "promotion_state", filter.promotionState);
    setParam(params, "outcome_status", filter.outcomeStatus);
    setParam(params, "run_id", filter.runID);
    setParam(params, "trace_id", filter.traceID);
    const data = await fetchJSON(`/api/v1/projects/${encodeURIComponent(projectID)}/evidence-graph/claims?${params}`, 12000);
    const claims = Array.isArray(data.claims) ? data.claims : [];
    const visible = filter.claimSearch ? claims.filter((claim) => evidenceClaimMatches(claim, filter.claimSearch)) : claims;
    state.claims = append ? state.claims.concat(visible) : visible;
    state.nextPageToken = data.next_page_token || data.NextPageToken || "";
    renderEvidenceClaims(projectID, state, listNode, detailNode);
  } catch (error) {
    if (!append) listNode.replaceChildren(emptyText(error.message));
  } finally {
    state.loading = false;
    renderEvidencePager(projectID, state, listNode, pagerNode, detailNode, fields);
  }
}

async function loadEvidenceClaimByID(projectID, claimID, state, listNode, detailNode) {
  try {
    const data = await fetchJSON(`/api/v1/projects/${encodeURIComponent(projectID)}/evidence-graph/claims/${encodeURIComponent(claimID)}`, 12000);
    const claim = data?.claim || null;
    state.claims = claim ? [claim] : [];
    state.nextPageToken = "";
    renderEvidenceClaims(projectID, state, listNode, detailNode);
    if (claim?.id) {
      state.selectedClaimID = claim.id;
      detailNode.replaceChildren(evidenceClaimChain(data));
    }
  } catch {
    state.claims = [];
    state.nextPageToken = "";
    renderEvidenceClaims(projectID, state, listNode, detailNode);
  }
}

function evidenceFilterValues(fields) {
  if (!fields) return {};
  return {
    claimSearch: fields.claimSearch.value.trim().toLowerCase(),
    artifactRef: fields.artifactRef.value.trim(),
    promotionState: fields.promotionState.value.trim(),
    outcomeStatus: fields.outcomeStatus.value.trim(),
    runID: fields.runID.value.trim(),
    traceID: fields.traceID.value.trim(),
  };
}

function setParam(params, key, value) {
  if (value) params.set(key, value);
}

function evidenceClaimMatches(claim, query) {
  const haystack = [
    claim?.id,
    claim?.claim_ref,
    claim?.summary,
    claim?.status,
    claim?.run_id,
    claim?.trace_id,
  ].map(safeEvidenceText).join(" ").toLowerCase();
  return haystack.includes(query);
}

function renderEvidenceClaims(projectID, state, listNode, detailNode) {
  clear(listNode);
  if (!state.claims.length) {
    listNode.append(emptyText("No evidence claims match the current filters."));
    return;
  }
  state.claims.forEach((claim) => {
    const claimID = safeEvidenceText(claim.id);
    listNode.append(el("button", {
      class: "integration-row",
      type: "button",
      disabled: !claimID,
      onClick: () => loadEvidenceClaimDetail(projectID, claimID, state, detailNode),
    },
      el("strong", { text: safeEvidenceText(claim.claim_ref || claim.id) || "claim" }),
      el("span", { text: compactJoin([claim.status, claim.run_id, claim.trace_id, formatDate(claim.updated_at || claim.created_at)]) }),
      el("span", { text: safeEvidenceSummary(claim.summary) }),
    ));
  });
}

function renderEvidencePager(projectID, state, listNode, pagerNode, detailNode, fields) {
  clear(pagerNode);
  if (state.loading) {
    pagerNode.append(el("span", { class: "muted-text", text: "Loading evidence claims..." }));
    return;
  }
  if (!state.nextPageToken) return;
  pagerNode.append(el("button", {
    type: "button",
    class: "compact",
    onClick: () => loadEvidenceClaims(projectID, state, listNode, pagerNode, detailNode, true, fields),
    text: "Load more",
  }));
}

async function loadEvidenceClaimDetail(projectID, claimID, state, detailNode) {
  state.selectedClaimID = claimID;
  detailNode.replaceChildren(emptyText("Loading claim chain..."));
  try {
    const data = await fetchJSON(`/api/v1/projects/${encodeURIComponent(projectID)}/evidence-graph/claims/${encodeURIComponent(claimID)}`, 12000);
    if (state.selectedClaimID !== claimID) return;
    detailNode.replaceChildren(evidenceClaimChain(data));
  } catch (error) {
    detailNode.replaceChildren(emptyText(error.message));
  }
}

function evidenceClaimChain(record) {
  const claim = record?.claim || {};
  return el("div", { class: "evidence-chain" },
    evidenceFlow(),
    infoList([
      ["Claim", safeEvidenceText(claim.claim_ref || claim.id)],
      ["Status", safeEvidenceText(claim.status)],
      ["Run", safeEvidenceText(claim.run_id)],
      ["Trace", safeEvidenceText(claim.trace_id)],
      ["Created", formatDate(claim.created_at)],
      ["Updated", formatDate(claim.updated_at)],
    ]),
    safeEvidenceSummary(claim.summary) ? block("Summary", el("p", { text: safeEvidenceSummary(claim.summary) })) : null,
    evidenceSection("Evidence", record?.evidence, evidenceRowEvidence),
    evidenceSection("Decisions", record?.decisions, evidenceRowDecision),
    evidenceSection("Actions", record?.actions, evidenceRowAction),
    evidenceSection("Outcomes", record?.outcomes, evidenceRowOutcome),
    evidenceSection("Artifacts", record?.artifact_links, evidenceRowArtifact),
    evidenceSection("Promotions", record?.promotion_links, evidenceRowPromotion),
  );
}

function evidenceFlow() {
  return el("div", { class: "flow" },
    ["Claim", "Evidence", "Decision", "Action", "Outcome"].map((label) => el("div", { class: "flow__node" },
      pill(label, "muted"),
      el("strong", { class: "flow__val", text: label }),
    )),
  );
}

function evidenceSection(title, items, renderer) {
  const rows = Array.isArray(items) ? items : [];
  return block(title, rows.length ? el("div", { class: "rows" }, rows.map(renderer)) : emptyText(`No ${title.toLowerCase()} recorded.`));
}

function evidenceRowEvidence(item) {
  return evidenceMetadataRow(
    safeEvidenceText(item.evidence_ref),
    compactJoin([item.evidence_kind, item.source_ref, formatDate(item.created_at)]),
    safeEvidenceSummary(item.summary),
  );
}

function evidenceRowDecision(item) {
  return evidenceMetadataRow(
    safeEvidenceText(item.decision_ref),
    compactJoin([item.state, item.verifier_ref, formatDate(item.decided_at)]),
    safeEvidenceSummary(item.rationale),
  );
}

function evidenceRowAction(item) {
  const paths = safeChangedPaths(item.changed_files);
  return evidenceMetadataRow(
    safeEvidenceText(item.action_ref),
    compactJoin([item.action_kind, item.run_id, formatDate(item.created_at)]),
    safeEvidenceSummary(item.summary),
    paths.length ? el("div", { class: "tag-list" }, paths.map((path) => el("span", { class: "tag", text: path }))) : null,
  );
}

function evidenceRowOutcome(item) {
  return evidenceMetadataRow(
    safeEvidenceText(item.outcome_ref),
    compactJoin([item.outcome_kind, item.status, item.verifier_ref, formatDate(item.created_at)]),
    safeEvidenceSummary(item.summary),
  );
}

function evidenceRowArtifact(item) {
  return evidenceMetadataRow(
    safeEvidenceText(item.artifact_ref),
    compactJoin([item.artifact_kind, item.run_id]),
    "",
  );
}

function evidenceRowPromotion(item) {
  return evidenceMetadataRow(
    safeEvidenceText(item.artifact_ref),
    compactJoin([item.promotion_state, item.run_id, item.verifier_ref, item.decision_ref, item.action_ref, item.outcome_ref, formatDate(item.decided_at)]),
    safeEvidenceText(item.source_ref),
  );
}

function evidenceMetadataRow(title, meta, summaryText, extra) {
  return el("div", { class: "row row--file" },
    el("strong", { text: title || "metadata" }),
    meta ? el("span", { text: meta }) : null,
    summaryText ? el("span", { text: summaryText }) : null,
    extra || null,
  );
}

function compactJoin(values) {
  return values.map(safeEvidenceText).filter(Boolean).join(" | ");
}

function safeEvidenceSummary(value) {
  const text = safeEvidenceText(value);
  return text.length > 500 ? `${text.slice(0, 500)}...` : text;
}

function safeEvidenceText(value) {
  if (value == null) return "";
  const text = String(value).replace(/\s+/g, " ").trim();
  if (!text || unsafeEvidenceText(text)) return "";
  return text;
}

function unsafeEvidenceText(text) {
  const lower = text.toLowerCase();
  return lower.includes("raw_prompt") ||
    lower.includes("raw prompt") ||
    lower.includes("raw_request") ||
    lower.includes("raw_result") ||
    lower.includes("raw stderr") ||
    lower.includes("raw source") ||
    lower.includes("source dump") ||
    lower.includes("package main") ||
    lower.includes("func ") ||
    lower.includes("provider payload") ||
    lower.includes("authorization:") ||
    lower.includes("bearer ") ||
    lower.includes("token=") ||
    lower.includes("secret=") ||
    lower.includes("wsl.localhost") ||
    lower.includes("http://") ||
    lower.includes("https://") ||
    lower.includes("/home/") ||
    /[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}/i.test(text) ||
    /\+?[0-9][0-9 .()-]{7,}[0-9]/.test(text) ||
    /^[a-z]:\\/i.test(text) ||
    /\\/.test(text);
}

function safeChangedPaths(paths) {
  if (!Array.isArray(paths)) return [];
  return paths
    .map(safeEvidenceText)
    .filter((path) => path && !path.startsWith("/") && !path.includes("..") && !path.includes(":"));
}

function tabConfidence(projectID) {
  const listNode = el("div", { class: "rows" }, emptyText("Loading confidence assessments..."));
  const detailNode = el("div", { class: "evidence-detail" }, emptyText("Select an assessment to inspect the score explanation."));
  const pagerNode = el("div", { class: "integration-pager" });
  const state = { assessments: [], nextPageToken: "", loading: false, selectedClaimID: "" };
  const controls = confidenceFilterForm(projectID, state, listNode, pagerNode, detailNode);
  const body = el("div", { class: "evidence-layout" },
    block("Assessments", controls, listNode, pagerNode),
    block("Score explanation", detailNode),
  );
  loadConfidenceAssessments(projectID, state, listNode, pagerNode, detailNode, false);
  return panel("Confidence", body);
}

function confidenceFilterForm(projectID, state, listNode, pagerNode, detailNode) {
  const claimSearch = evidenceInput("Claim id", "claim_id_search");
  const runID = evidenceInput("Run id", "run_id");
  const traceID = evidenceInput("Trace id", "trace_id");
  const minScore = el("input", { type: "number", min: "0", max: "100", name: "min_score", placeholder: "Min score", "aria-label": "Min score" });
  const maxScore = el("input", { type: "number", min: "0", max: "100", name: "max_score", placeholder: "Max score", "aria-label": "Max score" });
  const band = evidenceSelect("Band", "band", ["", "high", "medium", "low", "unknown"]);
  const recommendation = evidenceSelect("Recommendation", "recommendation", ["", "promote", "verify", "review", "reject", "insufficient_evidence"]);
  const fields = { claimSearch, runID, traceID, minScore, maxScore, band, recommendation };
  const form = el("form", {
    class: "evidence-filters",
    onSubmit: async (event) => {
      event.preventDefault();
      await loadConfidenceAssessments(projectID, state, listNode, pagerNode, detailNode, false, fields);
    },
  },
    claimSearch,
    band,
    recommendation,
    minScore,
    maxScore,
    runID,
    traceID,
    el("button", { type: "submit", class: "compact", text: "Apply" }),
    el("button", {
      type: "button",
      class: "secondary compact",
      onClick: () => scoreConfidenceClaim(projectID, fields, state, listNode, pagerNode, detailNode),
      text: "Score claim",
    }),
    el("button", {
      type: "button",
      class: "secondary compact",
      onClick: () => {
        Object.values(fields).forEach((field) => { field.value = ""; });
        loadConfidenceAssessments(projectID, state, listNode, pagerNode, detailNode, false, fields);
      },
      text: "Clear",
    }),
  );
  form.dataset.projectSubview = "confidence";
  return form;
}

async function scoreConfidenceClaim(projectID, fields, state, listNode, pagerNode, detailNode) {
  const claimID = fields.claimSearch.value.trim();
  if (!claimID) {
    detailNode.replaceChildren(emptyText("Enter a claim id before scoring."));
    return;
  }
  detailNode.replaceChildren(emptyText("Scoring claim confidence..."));
  try {
    const data = await fetchJSONWithOptions(`/api/v1/projects/${encodeURIComponent(projectID)}/confidence/claims/${encodeURIComponent(claimID)}/score`, 15000, {
      method: "POST",
      headers: { "Accept": "application/json", "Content-Type": "application/json" },
      body: "{}",
    });
    const assessment = data?.assessment || null;
    state.selectedClaimID = assessment?.claim_id || claimID;
    detailNode.replaceChildren(confidenceAssessmentDetail(assessment));
    await loadConfidenceAssessments(projectID, state, listNode, pagerNode, detailNode, false, fields);
  } catch (error) {
    detailNode.replaceChildren(emptyText(error.message));
  }
}

async function loadConfidenceAssessments(projectID, state, listNode, pagerNode, detailNode, append, fields) {
  if (state.loading) return;
  state.loading = true;
  renderConfidencePager(projectID, state, listNode, pagerNode, detailNode, fields);
  const filter = confidenceFilterValues(fields);
  if (!append) {
    state.nextPageToken = "";
    state.selectedClaimID = "";
    listNode.replaceChildren(emptyText("Loading confidence assessments..."));
    detailNode.replaceChildren(emptyText("Select an assessment to inspect the score explanation."));
  }
  try {
    if (!append && filter.claimSearch) {
      await loadConfidenceAssessmentByClaim(projectID, filter.claimSearch, state, listNode, detailNode);
      return;
    }
    const params = new URLSearchParams({ page_size: "20" });
    if (append && state.nextPageToken) params.set("page_token", state.nextPageToken);
    setParam(params, "band", filter.band);
    setParam(params, "min_score", filter.minScore);
    setParam(params, "max_score", filter.maxScore);
    setParam(params, "recommendation", filter.recommendation);
    setParam(params, "run_id", filter.runID);
    setParam(params, "trace_id", filter.traceID);
    const data = await fetchJSON(`/api/v1/projects/${encodeURIComponent(projectID)}/confidence/claims?${params}`, 12000);
    const assessments = Array.isArray(data.assessments) ? data.assessments : [];
    state.assessments = append ? state.assessments.concat(assessments) : assessments;
    state.nextPageToken = data.next_page_token || data.NextPageToken || "";
    renderConfidenceAssessments(projectID, state, listNode, detailNode);
  } catch (error) {
    if (!append) listNode.replaceChildren(emptyText(error.message));
  } finally {
    state.loading = false;
    renderConfidencePager(projectID, state, listNode, pagerNode, detailNode, fields);
  }
}

async function loadConfidenceAssessmentByClaim(projectID, claimID, state, listNode, detailNode) {
  try {
    const assessment = await fetchJSON(`/api/v1/projects/${encodeURIComponent(projectID)}/confidence/claims/${encodeURIComponent(claimID)}`, 12000);
    state.assessments = assessment?.claim_id ? [assessment] : [];
    state.nextPageToken = "";
    renderConfidenceAssessments(projectID, state, listNode, detailNode);
    if (assessment?.claim_id) {
      state.selectedClaimID = assessment.claim_id;
      detailNode.replaceChildren(confidenceAssessmentDetail(assessment));
    }
  } catch {
    state.assessments = [];
    state.nextPageToken = "";
    renderConfidenceAssessments(projectID, state, listNode, detailNode);
  }
}

function confidenceFilterValues(fields) {
  if (!fields) return {};
  return {
    claimSearch: fields.claimSearch.value.trim(),
    runID: fields.runID.value.trim(),
    traceID: fields.traceID.value.trim(),
    minScore: confidenceScoreValue(fields.minScore.value),
    maxScore: confidenceScoreValue(fields.maxScore.value),
    band: fields.band.value.trim(),
    recommendation: fields.recommendation.value.trim(),
  };
}

function confidenceScoreValue(value) {
  const text = String(value || "").trim();
  if (text === "") return "";
  const number = Number(text);
  if (!Number.isInteger(number) || number < 0 || number > 100) return "";
  return String(number);
}

function renderConfidenceAssessments(projectID, state, listNode, detailNode) {
  clear(listNode);
  if (!state.assessments.length) {
    listNode.append(emptyText("No confidence assessments match the current filters."));
    return;
  }
  state.assessments.forEach((assessment) => {
    const claimID = safeEvidenceText(assessment.claim_id);
    listNode.append(el("button", {
      class: "integration-row",
      type: "button",
      disabled: !claimID,
      onClick: () => loadConfidenceAssessmentDetail(projectID, claimID, state, detailNode),
    },
      el("strong", { text: confidenceAssessmentTitle(assessment) }),
      el("span", { text: compactJoin([`score ${confidenceScoreText(assessment.score)}`, assessment.band, assessment.recommendation]) }),
      el("span", { text: compactJoin([assessment.claim_ref, assessment.run_id, assessment.trace_id, formatDate(assessment.updated_at || assessment.created_at)]) }),
    ));
  });
}

function renderConfidencePager(projectID, state, listNode, pagerNode, detailNode, fields) {
  clear(pagerNode);
  if (state.loading) {
    pagerNode.append(el("span", { class: "muted-text", text: "Loading confidence assessments..." }));
    return;
  }
  if (!state.nextPageToken) return;
  pagerNode.append(el("button", {
    type: "button",
    class: "compact",
    onClick: () => loadConfidenceAssessments(projectID, state, listNode, pagerNode, detailNode, true, fields),
    text: "Load more",
  }));
}

async function loadConfidenceAssessmentDetail(projectID, claimID, state, detailNode) {
  state.selectedClaimID = claimID;
  detailNode.replaceChildren(emptyText("Loading confidence assessment..."));
  try {
    const assessment = await fetchJSON(`/api/v1/projects/${encodeURIComponent(projectID)}/confidence/claims/${encodeURIComponent(claimID)}`, 12000);
    if (state.selectedClaimID !== claimID) return;
    detailNode.replaceChildren(confidenceAssessmentDetail(assessment));
  } catch (error) {
    detailNode.replaceChildren(emptyText(error.message));
  }
}

function confidenceAssessmentDetail(assessment) {
  if (!assessment) return emptyText("Confidence assessment unavailable.");
  return el("div", { class: "evidence-chain" },
    infoList([
      ["Score", confidenceScoreText(assessment.score)],
      ["Band", safeEvidenceText(assessment.band)],
      ["Recommendation", safeEvidenceText(assessment.recommendation)],
      ["Claim", safeEvidenceText(assessment.claim_ref || assessment.claim_id)],
      ["Run", safeEvidenceText(assessment.run_id)],
      ["Trace", safeEvidenceText(assessment.trace_id)],
      ["Created", formatDate(assessment.created_at)],
      ["Updated", formatDate(assessment.updated_at)],
    ]),
    confidenceLinkedClaim(assessment),
    confidenceInputsBlock(assessment.inputs),
    evidenceSection("Factors", assessment.factors, confidenceFactorRow),
  );
}

function confidenceLinkedClaim(assessment) {
  const claimID = safeEvidenceText(assessment.claim_id);
  if (!claimID) return null;
  const { projectID } = selectedRoute();
  return el("button", {
    type: "button",
    class: "secondary compact",
    onClick: () => {
      location.hash = `project=${encodeURIComponent(projectID)}&tab=evidence`;
      setTimeout(() => {
        const input = document.querySelector("[data-project-subview='evidence-graph'] input[name='claim_id_search']");
        const form = input && input.closest("form");
        if (input && form) {
          input.value = claimID;
          form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
        }
      }, 0);
    },
    text: "Open Evidence Graph claim",
  });
}

function confidenceInputsBlock(inputs) {
  const value = inputs || {};
  return block("Inputs",
    infoList([
      ["Evidence", numberValue(value.evidence_count)],
      ["Evidence kinds", safeEvidenceText((value.evidence_kinds || []).join(", "))],
      ["Decisions", numberValue(value.decision_count)],
      ["Actions", numberValue(value.action_count)],
      ["Passed outcomes", numberValue(value.passed_outcome_count)],
      ["Failed outcomes", numberValue(value.failed_outcome_count)],
      ["Promotion", safeEvidenceText(value.promotion_state)],
      ["Context", compactJoin([value.context_health_status, value.context_health_reason])],
      ["Claim checks", `${numberValue(value.claim_check_verified)} verified | ${numberValue(value.claim_check_actionable)} actionable`],
      ["Impact", confidenceImpactText(value)],
    ]),
  );
}

function confidenceFactorRow(item) {
  return evidenceMetadataRow(
    safeEvidenceText(item.name),
    compactJoin([item.status, `delta ${confidenceDeltaText(item.score_delta)}`, `weight ${confidenceScoreText(item.weight)}`, item.source_ref]),
    safeEvidenceSummary(item.summary),
  );
}

function confidenceAssessmentTitle(assessment) {
  const claim = safeEvidenceText(assessment.claim_ref || assessment.claim_id);
  return claim || "confidence assessment";
}

function confidenceScoreText(value) {
  return typeof value === "number" ? String(value) : "unknown";
}

function confidenceDeltaText(value) {
  if (typeof value !== "number") return "unknown";
  return value > 0 ? `+${value}` : String(value);
}

function confidenceImpactText(inputs) {
  return compactJoin([
    inputs?.impact_partial ? "partial" : "complete",
    `${numberValue(inputs?.impact_residual_unknown_count)} unknowns`,
    `${numberValue(inputs?.impact_security_flag_count)} security flags`,
  ]);
}

function tabKnowledgePromotion(projectID) {
  const projectListNode = el("div", { class: "rows" }, emptyText("Loading project knowledge..."));
  const orgListNode = el("div", { class: "rows" }, emptyText("Loading org knowledge..."));
  const projectPagerNode = el("div", { class: "integration-pager" });
  const orgPagerNode = el("div", { class: "integration-pager" });
  const detailNode = el("div", { class: "knowledge-detail" }, emptyText("Select knowledge to inspect safe promotion metadata."));
  const state = {
    project: { records: [], nextPageToken: "", loading: false },
    org: { records: [], nextPageToken: "", loading: false },
    selectedKnowledgeID: "",
  };
  const controls = knowledgeFilterForm(projectID, state, projectListNode, orgListNode, projectPagerNode, orgPagerNode, detailNode);
  const body = el("div", { class: "knowledge-view" },
    controls,
    el("div", { class: "knowledge-layout" },
      block("Project-level knowledge", projectListNode, projectPagerNode),
      block("Org-level knowledge",
        el("p", { class: "knowledge-note", text: "Org promotion requires explicit review and is never automatic." }),
        orgListNode,
        orgPagerNode),
      block("Selected knowledge", detailNode),
    ),
  );
  loadKnowledgeLists(projectID, state, projectListNode, orgListNode, projectPagerNode, orgPagerNode, detailNode, false);
  return panel("Knowledge Promotion", body);
}

function knowledgeFilterForm(projectID, state, projectListNode, orgListNode, projectPagerNode, orgPagerNode, detailNode) {
  const scope = evidenceSelect("Scope", "scope", ["", "project", "org"]);
  const stateSelect = evidenceSelect("State", "state", ["", "candidate", "validated", "project_promoted", "org_review", "org_promoted", "rejected", "superseded"]);
  const claimID = evidenceInput("Claim id", "claim_id");
  const knowledgeRef = evidenceInput("Knowledge ref", "knowledge_ref");
  const confidenceBand = evidenceSelect("Confidence band", "confidence_band", ["", "high", "medium", "low", "unknown"]);
  const minConfidence = el("input", { type: "number", min: "0", max: "100", name: "min_confidence", placeholder: "Min confidence", "aria-label": "Min confidence" });
  const maxConfidence = el("input", { type: "number", min: "0", max: "100", name: "max_confidence", placeholder: "Max confidence", "aria-label": "Max confidence" });
  const fields = { scope, stateSelect, claimID, knowledgeRef, confidenceBand, minConfidence, maxConfidence };
  const form = el("form", {
    class: "knowledge-filters",
    onSubmit: async (event) => {
      event.preventDefault();
      await loadKnowledgeLists(projectID, state, projectListNode, orgListNode, projectPagerNode, orgPagerNode, detailNode, false, fields);
    },
  },
    scope,
    stateSelect,
    claimID,
    knowledgeRef,
    confidenceBand,
    minConfidence,
    maxConfidence,
    el("button", { type: "submit", class: "compact", text: "Apply" }),
    el("button", {
      type: "button",
      class: "secondary compact",
      onClick: () => {
        Object.values(fields).forEach((field) => { field.value = ""; });
        loadKnowledgeLists(projectID, state, projectListNode, orgListNode, projectPagerNode, orgPagerNode, detailNode, false, fields);
      },
      text: "Clear",
    }),
  );
  form.dataset.projectSubview = "knowledge-promotion";
  return form;
}

async function loadKnowledgeLists(projectID, state, projectListNode, orgListNode, projectPagerNode, orgPagerNode, detailNode, append, fields) {
  const filter = knowledgeFilterValues(fields);
  if (!append) {
    state.selectedKnowledgeID = "";
    detailNode.replaceChildren(emptyText("Select knowledge to inspect safe promotion metadata."));
  }
  const loads = [];
  if (filter.scope !== "org") {
    loads.push(loadKnowledgeList("project", projectID, state.project, projectListNode, projectPagerNode, detailNode, append, filter));
  } else if (!append) {
    state.project.records = [];
    state.project.nextPageToken = "";
    projectListNode.replaceChildren(emptyText("Project list skipped by scope filter."));
    clear(projectPagerNode);
  }
  if (filter.scope !== "project") {
    loads.push(loadKnowledgeList("org", projectID, state.org, orgListNode, orgPagerNode, detailNode, append, filter));
  } else if (!append) {
    state.org.records = [];
    state.org.nextPageToken = "";
    orgListNode.replaceChildren(emptyText("Org list skipped by scope filter."));
    clear(orgPagerNode);
  }
  await Promise.all(loads);
}

async function loadKnowledgeList(scope, projectID, bucket, listNode, pagerNode, detailNode, append, filter) {
  if (bucket.loading) return;
  bucket.loading = true;
  renderKnowledgePager(scope, projectID, bucket, listNode, pagerNode, detailNode, filter);
  if (!append) {
    bucket.records = [];
    bucket.nextPageToken = "";
    listNode.replaceChildren(emptyText(scope === "org" ? "Loading org knowledge..." : "Loading project knowledge..."));
  }
  if (scope === "org" && filter.state && filter.state !== "org_promoted") {
    bucket.loading = false;
    bucket.records = [];
    bucket.nextPageToken = "";
    listNode.replaceChildren(emptyText("Org knowledge only lists explicitly org-promoted records."));
    renderKnowledgePager(scope, projectID, bucket, listNode, pagerNode, detailNode, filter);
    return;
  }
  try {
    const params = knowledgeQueryParams(scope, append ? bucket.nextPageToken : "", filter);
    const url = scope === "org"
      ? `/api/v1/orgs/default/knowledge?${params}`
      : `/api/v1/projects/${encodeURIComponent(projectID)}/knowledge?${params}`;
    const data = await fetchJSON(url, 12000);
    const records = Array.isArray(data.knowledge) ? data.knowledge : [];
    bucket.records = append ? bucket.records.concat(records) : records;
    bucket.nextPageToken = data.next_page_token || data.NextPageToken || "";
    renderKnowledgeRecords(scope, projectID, bucket, listNode, detailNode);
  } catch (error) {
    if (!append) listNode.replaceChildren(emptyText(error.message));
  } finally {
    bucket.loading = false;
    renderKnowledgePager(scope, projectID, bucket, listNode, pagerNode, detailNode, filter);
  }
}

function knowledgeFilterValues(fields) {
  if (!fields) return {};
  return {
    scope: fields.scope.value.trim(),
    state: fields.stateSelect.value.trim(),
    claimID: fields.claimID.value.trim(),
    knowledgeRef: fields.knowledgeRef.value.trim(),
    confidenceBand: fields.confidenceBand.value.trim(),
    minConfidence: confidenceScoreValue(fields.minConfidence.value),
    maxConfidence: confidenceScoreValue(fields.maxConfidence.value),
  };
}

function knowledgeQueryParams(scope, pageToken, filter) {
  const params = new URLSearchParams({ page_size: "20" });
  if (pageToken) params.set("page_token", pageToken);
  setParam(params, "scope", scope);
  setParam(params, "state", scope === "org" ? "org_promoted" : filter.state);
  setParam(params, "claim_id", filter.claimID);
  setParam(params, "knowledge_ref", filter.knowledgeRef);
  setParam(params, "confidence_band", filter.confidenceBand);
  setParam(params, "min_confidence", filter.minConfidence);
  setParam(params, "max_confidence", filter.maxConfidence);
  return params;
}

function renderKnowledgeRecords(scope, projectID, bucket, listNode, detailNode) {
  clear(listNode);
  if (!bucket.records.length) {
    listNode.append(emptyText(`No ${scope} knowledge matches the current filters.`));
    return;
  }
  bucket.records.forEach((record) => {
    const knowledgeID = safeKnowledgeText(record.id);
    const recordProjectID = safeKnowledgeText(record.project_id) || projectID;
    listNode.append(el("button", {
      class: `knowledge-row knowledge-row--${scope}`,
      type: "button",
      disabled: !knowledgeID,
      onClick: () => loadKnowledgeRecordDetail(projectID, recordProjectID, knowledgeID, detailNode),
    },
      el("div", { class: "knowledge-row__head" },
        el("strong", { text: safeKnowledgeText(record.knowledge_ref || record.id) || "knowledge" }),
        knowledgeScopePill(record.scope || scope),
      ),
      el("span", { text: compactJoin([record.state, `${confidenceScoreText(record.confidence_score)} ${record.confidence_band || ""}`, record.claim_ref || record.claim_id]) }),
      el("span", { text: safeKnowledgeSummary(record.summary) }),
    ));
  });
}

function renderKnowledgePager(scope, projectID, bucket, listNode, pagerNode, detailNode, filter) {
  clear(pagerNode);
  if (bucket.loading) {
    pagerNode.append(el("span", { class: "muted-text", text: `Loading ${scope} knowledge...` }));
    return;
  }
  if (!bucket.nextPageToken) return;
  pagerNode.append(el("button", {
    type: "button",
    class: "compact",
    onClick: () => loadKnowledgeList(scope, projectID, bucket, listNode, pagerNode, detailNode, true, filter),
    text: "Load more",
  }));
}

async function loadKnowledgeRecordDetail(currentProjectID, recordProjectID, knowledgeID, detailNode) {
  detailNode.replaceChildren(emptyText("Loading knowledge metadata..."));
  try {
    const record = await fetchJSON(`/api/v1/projects/${encodeURIComponent(recordProjectID || currentProjectID)}/knowledge/${encodeURIComponent(knowledgeID)}`, 12000);
    detailNode.replaceChildren(knowledgeRecordDetail(currentProjectID, record));
  } catch (error) {
    detailNode.replaceChildren(emptyText(error.message));
  }
}

function knowledgeRecordDetail(currentProjectID, record) {
  if (!record) return emptyText("Knowledge record unavailable.");
  const scope = safeKnowledgeText(record.scope);
  return el("div", { class: `knowledge-chain knowledge-chain--${scope || "project"}` },
    el("div", { class: "knowledge-detail__head" },
      knowledgeScopePill(scope),
      pill(safeKnowledgeText(record.state) || "state unknown", record.state === "org_promoted" ? "warn" : "ok"),
    ),
    infoList([
      ["Knowledge", safeKnowledgeText(record.knowledge_ref || record.id)],
      ["Claim", safeKnowledgeText(record.claim_ref || record.claim_id)],
      ["Confidence", compactJoin([confidenceScoreText(record.confidence_score), record.confidence_band])],
      ["Assessment", safeKnowledgeText(record.confidence_assessment_id)],
      ["Project", safeKnowledgeText(record.project_id)],
      ["Org", safeKnowledgeText(record.org_ref)],
      ["Created", formatDate(record.created_at)],
      ["Updated", formatDate(record.updated_at)],
      ["Promoted", formatDate(record.promoted_at)],
    ]),
    knowledgeTextBlock("Summary", record.summary),
    knowledgeTextBlock("Reuse guidance", record.reuse_guidance),
    knowledgeRefsBlock("Evidence refs", record.evidence_refs),
    knowledgeRefsBlock("Verifier refs", record.verifier_refs),
    knowledgeRefsBlock("Outcome refs", record.outcome_refs),
    knowledgeRefsBlock("Promotion decisions", record.promotion_decisions || record.decisions || record.promotion_refs, knowledgePromotionDecisionRow),
    knowledgeSupersessionBlock(record),
    knowledgeRefsBlock("Reuse events", record.reuse_events, knowledgeReuseEventRow),
    knowledgeReuseForm(currentProjectID, record),
  );
}

function knowledgeTextBlock(title, value) {
  const text = safeKnowledgeSummary(value);
  return block(title, text ? el("p", { text }) : emptyText(`No ${title.toLowerCase()} recorded.`));
}

function knowledgeRefsBlock(title, values, renderer) {
  const items = Array.isArray(values) ? values : [];
  if (!items.length) return block(title, emptyText(`No ${title.toLowerCase()} recorded.`));
  const rowRenderer = renderer || ((item) => evidenceMetadataRow(safeKnowledgeText(item), "", ""));
  return block(title, el("div", { class: "rows" }, items.map(rowRenderer)));
}

function knowledgePromotionDecisionRow(item) {
  if (typeof item === "string") return evidenceMetadataRow(safeKnowledgeText(item), "", "");
  return evidenceMetadataRow(
    safeKnowledgeText(item.decision_ref || item.id),
    compactJoin([item.from_state, item.to_state, item.scope, item.verifier_ref, confidenceScoreText(item.confidence_score), formatDate(item.decided_at)]),
    safeKnowledgeSummary(item.rationale),
  );
}

function knowledgeReuseEventRow(item) {
  if (typeof item === "string") return evidenceMetadataRow(safeKnowledgeText(item), "", "");
  return evidenceMetadataRow(
    safeKnowledgeText(item.reuse_ref || item.id),
    compactJoin([item.outcome, item.revalidated ? "revalidated" : "not revalidated", item.revalidation_ref, item.agent_run_id, item.trace_id, formatDate(item.created_at)]),
    safeKnowledgeSummary(item.summary),
  );
}

function knowledgeSupersessionBlock(record) {
  return block("Supersession state", infoList([
    ["Supersedes", safeKnowledgeText(record.supersedes_ref) || "none"],
    ["Superseded by", safeKnowledgeText(record.superseded_by_ref) || "none"],
  ]));
}

function knowledgeReuseForm(currentProjectID, record) {
  const knowledgeID = safeKnowledgeText(record.id);
  const recordProjectID = safeKnowledgeText(record.project_id) || currentProjectID;
  if (!knowledgeID) return null;
  const reuseRef = evidenceInput("Reuse ref", "reuse_ref");
  const revalidationRef = evidenceInput("Revalidation ref", "revalidation_ref");
  const outcome = evidenceSelect("Outcome", "outcome", ["used", "skipped", "stale", "contradicted"]);
  const revalidated = el("label", { class: "knowledge-check" },
    el("input", { type: "checkbox", name: "revalidated" }),
    el("span", { text: "Revalidated" }),
  );
  const summaryInput = evidenceInput("Safe summary", "summary");
  const status = el("span", { class: "muted-text" });
  const form = el("form", {
    class: "knowledge-reuse-form",
    onSubmit: async (event) => {
      event.preventDefault();
      const body = {
        reuse_ref: reuseRef.value.trim(),
        revalidated: Boolean(revalidated.querySelector("input")?.checked),
        revalidation_ref: revalidationRef.value.trim(),
        outcome: outcome.value.trim(),
        summary: summaryInput.value.trim(),
      };
      if (!body.reuse_ref) {
        status.textContent = "Reuse ref is required.";
        return;
      }
      status.textContent = "Recording reuse event...";
      try {
        const eventRecord = await fetchJSONWithOptions(`/api/v1/projects/${encodeURIComponent(recordProjectID)}/knowledge/${encodeURIComponent(knowledgeID)}/reuse-events`, 12000, {
          method: "POST",
          headers: { "Accept": "application/json", "Content-Type": "application/json" },
          body: JSON.stringify(body),
        });
        record.reuse_events = Array.isArray(record.reuse_events) ? record.reuse_events.concat(eventRecord) : [eventRecord];
        status.textContent = "Reuse event recorded.";
      } catch (error) {
        status.textContent = error.message;
      }
    },
  },
    reuseRef,
    outcome,
    revalidated,
    revalidationRef,
    summaryInput,
    el("button", { type: "submit", class: "compact", text: "Record reuse" }),
    status,
  );
  return block("Record reuse event", form);
}

function knowledgeScopePill(scope) {
  const value = safeKnowledgeText(scope) || "project";
  return el("span", { class: `scope-pill scope-pill--${value === "org" ? "org" : "project"}`, text: value === "org" ? "Org scope" : "Project scope" });
}

function safeKnowledgeText(value) {
  return safeEvidenceText(value);
}

function safeKnowledgeSummary(value) {
  return safeEvidenceSummary(value);
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
  const recentJira = Array.isArray(integrationSummary?.recent_jira_issues) ? integrationSummary.recent_jira_issues : [];
  const confluencePages = Array.isArray(integrationSummary?.confluence_pages) ? integrationSummary.confluence_pages : [];
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
  const hasJira = hasIntegrationProvider(project, providers, counts, "jira") || recentJira.length > 0;
  const hasConfluence = hasIntegrationProvider(project, providers, counts, "confluence") || confluencePages.length > 0;
  if (hasJira) children.push(block("Recent Jira issues", integrationBrowser(project.id, "jira", "issues", recentJira)));
  if (hasConfluence) children.push(block("Confluence pages", integrationBrowser(project.id, "confluence", "pages", confluencePages)));
  if (hasJira || hasConfluence) children.push(block("Search", integrationSearch(project.id)));
  return panel("Integrations", ...children);
}

function hasIntegrationProvider(project, providers, counts, provider) {
  return Boolean(project.integrations?.[provider]) ||
    providers.some((item) => item.provider === provider) ||
    counts.some((item) => item.provider === provider && item.count > 0);
}

function integrationBrowser(projectID, provider, collection, initialItems) {
  const listNode = el("div", { class: "rows" });
  const pagerNode = el("div", { class: "integration-pager" });
  const state = { items: Array.isArray(initialItems) ? initialItems.slice() : [], nextPageToken: "", loading: false };
  const node = el("div", { class: "integration-browser", dataset: { provider, collection } }, listNode, pagerNode);
  renderIntegrationItems(projectID, provider, collection, listNode, state.items);
  renderIntegrationPager(projectID, provider, collection, state, listNode, pagerNode);
  loadIntegrationItems(projectID, provider, collection, state, listNode, pagerNode, false);
  return node;
}

function renderIntegrationItems(projectID, provider, collection, listNode, items) {
  clear(listNode);
  if (!Array.isArray(items) || items.length === 0) {
    listNode.append(emptyText(`No indexed ${provider} ${collection}.`));
    return;
  }
  items.forEach((item) => {
    const id = integrationItemID(item);
    const title = integrationItemTitle(item);
    const status = integrationItemField(item, "item_status", "ItemStatus") || "unknown";
    const updated = integrationItemField(item, "item_updated_at", "ItemUpdatedAt");
    const key = integrationItemField(item, "item_key", "ItemKey");
    const type = integrationItemField(item, "item_type", "ItemType");
    listNode.append(el("button", {
      class: "integration-row",
      type: "button",
      disabled: !id,
      onClick: () => openIntegrationDrawer(projectID, provider, collection, id, item),
    },
      el("strong", { text: title }),
      el("span", { text: [key && key !== title ? key : "", type, status, `updated ${formatDate(updated)}`].filter(Boolean).join(" · ") }),
    ));
  });
}

async function loadIntegrationItems(projectID, provider, collection, state, listNode, pagerNode, append) {
  const path = provider === "jira" ? "jira/issues" : "confluence/pages";
  if (state.loading) return;
  state.loading = true;
  renderIntegrationPager(projectID, provider, collection, state, listNode, pagerNode);
  try {
    const params = new URLSearchParams({ page_size: "12", sort: "updated_desc" });
    if (append && state.nextPageToken) params.set("page_token", state.nextPageToken);
    const data = await fetchJSON(`/api/v1/projects/${encodeURIComponent(projectID)}/integrations/${path}?${params}`, 12000);
    const items = Array.isArray(data.items) ? data.items : [];
    state.items = append ? state.items.concat(items) : items;
    state.nextPageToken = data.next_page_token || data.NextPageToken || "";
    renderIntegrationItems(projectID, provider, collection, listNode, state.items);
  } catch (error) {
    if (!listNode.childElementCount) listNode.append(emptyText(`${provider} ${collection} unavailable.`));
  } finally {
    state.loading = false;
    renderIntegrationPager(projectID, provider, collection, state, listNode, pagerNode);
  }
}

function renderIntegrationPager(projectID, provider, collection, state, listNode, pagerNode) {
  clear(pagerNode);
  if (state.loading) {
    pagerNode.append(el("span", { class: "muted-text", text: "Loading indexed entries..." }));
    return;
  }
  if (!state.nextPageToken) return;
  pagerNode.append(el("button", {
    type: "button",
    class: "compact",
    onClick: () => loadIntegrationItems(projectID, provider, collection, state, listNode, pagerNode, true),
    text: "Load more",
  }));
}

function openIntegrationDrawer(projectID, provider, collection, id, item) {
  document.querySelectorAll(".integration-detail").forEach((node) => node.remove());
  const body = el("div", { class: "integration-detail__body" }, emptyText("Loading indexed content..."));
  const drawer = el("aside", { class: "integration-detail", role: "dialog", "aria-modal": "true", "aria-label": "Indexed integration content" },
    el("div", { class: "integration-detail__head" },
      el("div", {},
        el("strong", { text: integrationItemTitle(item) }),
        el("span", { text: integrationDrawerSubtitle(provider, item) }),
      ),
      el("button", { class: "icon-btn", type: "button", title: "Close", "aria-label": "Close", onClick: () => drawer.remove(), text: "x" }),
    ),
    body,
  );
  document.body.append(drawer);
  loadIntegrationPreview(projectID, provider, collection, id, body);
}

async function loadIntegrationPreview(projectID, provider, collection, id, previewNode) {
  const path = provider === "jira" ? `jira/issues/${encodeURIComponent(id)}` : `confluence/pages/${encodeURIComponent(id)}`;
  previewNode.replaceChildren(emptyText("Loading indexed preview..."));
  try {
    const data = await fetchJSON(`/api/v1/projects/${encodeURIComponent(projectID)}/integrations/${path}?max_chunks=4&max_chunk_bytes=1200`, 5000);
    const chunks = Array.isArray(data.chunks) ? data.chunks : Array.isArray(data.Chunks) ? data.Chunks : [];
    previewNode.replaceChildren(
      chunks.length
        ? el("div", { class: "integration-chunks" }, chunks.map((chunk) => el("section", { class: "integration-chunk" },
          el("strong", { text: integrationChunkLabel(chunk) }),
          el("pre", { text: integrationObjectField(chunk, "text", "Text") || "" }),
        )))
        : emptyText(`No indexed ${collection} content for this item.`),
    );
  } catch (error) {
    previewNode.replaceChildren(emptyText(error.message));
  }
}
function integrationSearch(projectID) {
  const input = el("input", { type: "search", placeholder: "Search indexed Jira and Confluence", "aria-label": "Search indexed integrations" });
  const provider = el("select", { "aria-label": "Integration provider" },
    el("option", { value: "", text: "All" }),
    el("option", { value: "jira", text: "Jira" }),
    el("option", { value: "confluence", text: "Confluence" }),
  );
  const results = el("div", { class: "search-results" }, emptyText("Enter a query to search indexed integration content."));
  const form = el("form", {
    class: "integration-search",
    onSubmit: async (event) => {
      event.preventDefault();
      const query = input.value.trim();
      if (!query) {
        results.replaceChildren(emptyText("Enter a query to search indexed integration content."));
        return;
      }
      results.replaceChildren(emptyText("Searching indexed integrations..."));
      const params = new URLSearchParams({ query, max_results: "12", max_snippet_bytes: "360" });
      if (provider.value) params.set("provider", provider.value);
      try {
        const data = await fetchJSON(`/api/v1/projects/${encodeURIComponent(projectID)}/integrations/search?${params}`, 6000);
        renderIntegrationSearchResults(results, Array.isArray(data.results) ? data.results : []);
      } catch (error) {
        results.replaceChildren(emptyText(error.message));
      }
    },
  }, input, provider, el("button", { type: "submit", class: "compact", text: "Search" }));
  return el("div", { class: "integration-search-wrap" }, form, results);
}

function renderIntegrationSearchResults(node, results) {
  clear(node);
  if (!results.length) {
    node.append(emptyText("No indexed integration matches."));
    return;
  }
  node.append(el("div", { class: "rows" }, results.map((result) => {
    const artifact = result.artifact || result.Artifact || {};
    const provider = integrationObjectField(artifact, "provider", "Provider") || result.provider || result.Provider || "integration";
    const itemID = integrationObjectField(artifact, "item_id", "ItemID") || result.item_id || result.ItemID || "";
    const label = integrationObjectField(artifact, "item_key", "ItemKey") || itemID || provider;
    const snippet = result.snippet || result.Snippet || "";
    return el("div", { class: "row row--file" },
      el("strong", { text: `${provider}: ${label}` }),
      el("span", { text: snippet }),
    );
  })));
}

function integrationItemField(item, snake, title) {
  return item?.[snake] ?? item?.[title] ?? "";
}

function integrationItemID(item) {
  return integrationItemField(item, "item_key", "ItemKey") || integrationItemField(item, "item_id", "ItemID");
}

function integrationItemTitle(item) {
  const title = integrationItemField(item, "title", "Title");
  const key = integrationItemField(item, "item_key", "ItemKey");
  const id = integrationItemField(item, "item_id", "ItemID");
  if (key && title) return `${key} · ${title}`;
  return title || key || id ||
    "indexed item";
}

function integrationDrawerSubtitle(provider, item) {
  const key = integrationItemField(item, "item_key", "ItemKey");
  const id = integrationItemField(item, "item_id", "ItemID");
  const type = integrationItemField(item, "item_type", "ItemType");
  return [provider, key, id && id !== key ? id : "", type].filter(Boolean).join(" · ");
}

function integrationObjectField(object, snake, title) {
  return object?.[snake] ?? object?.[title] ?? "";
}

function integrationChunkLabel(chunk) {
  return integrationObjectField(chunk, "label", "Label") ||
    integrationObjectField(chunk, "field_name", "FieldName") ||
    "content";
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
  return fetchJSONWithOptions(url, timeoutMs, { headers: { "Accept": "application/json" } });
}

async function fetchJSONWithOptions(url, timeoutMs, options = {}) {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const response = await fetch(url, { ...options, signal: controller.signal });
    if (!response.ok) throw new Error(`${url} returned ${response.status}`);
    return await response.json();
  } catch (error) {
    if (error.name === "AbortError") throw new Error(`${url} timed out`);
    throw error;
  } finally {
    clearTimeout(timeout);
  }
}
