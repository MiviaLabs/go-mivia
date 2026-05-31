const projectsBody = document.querySelector("#projects");
const summary = document.querySelector("#summary");
const statusBox = document.querySelector("#status");
const refresh = document.querySelector("#refresh");

refresh.addEventListener("click", loadDashboard);

loadDashboard();

async function loadDashboard() {
  refresh.disabled = true;
  statusBox.textContent = "";
  projectsBody.innerHTML = "";
  summary.textContent = "Loading projects";

  try {
    const data = await fetchJSON("/api/v1/projects", 4000);
    const projects = Array.isArray(data.projects) ? data.projects : [];
    summary.textContent = `${projects.length} configured project${projects.length === 1 ? "" : "s"}`;
    renderProjects(projects);
    await loadOptionalStatus(projects);
  } catch (error) {
    summary.textContent = "Projects unavailable";
    statusBox.textContent = error.message;
  } finally {
    refresh.disabled = false;
  }
}

function renderProjects(projects) {
  if (projects.length === 0) {
    projectsBody.innerHTML = `<tr><td colspan="6" class="empty">No projects configured</td></tr>`;
    return;
  }

  projectsBody.replaceChildren(...projects.map((project) => {
    const row = document.createElement("tr");
    row.dataset.projectId = project.id;
    row.innerHTML = `
      <td>
        <strong>${escapeHTML(project.display_name || project.id)}</strong>
        <span>${escapeHTML(project.id)}</span>
        ${aliases(project)}
      </td>
      <td>${pill(project.enabled ? "enabled" : "disabled", project.enabled ? "ok" : "muted")} ${pill(project.validation_status || "unknown", project.validation_status === "valid" ? "ok" : "warn")}</td>
      <td>${escapeHTML(project.graph_storage)}<span>${escapeHTML(project.digest_mode)} / ${escapeHTML(project.update_policy)}</span></td>
      <td>${escapeHTML(project.workspace_mode)}</td>
      <td>${integrations(project.integrations)}</td>
      <td class="context">Loading</td>
    `;
    return row;
  }));
}

async function loadOptionalStatus(projects) {
  await Promise.all(projects.map(async (project) => {
    const cell = document.querySelector(`tr[data-project-id="${cssEscape(project.id)}"] .context`);
    if (!cell) return;

    try {
      const [health, latest] = await Promise.allSettled([
        fetchJSON(`/api/v1/projects/${encodeURIComponent(project.id)}/context-health`, 3000),
        fetchJSON(`/api/v1/projects/${encodeURIComponent(project.id)}/ingestion-runs/latest`, 3000),
      ]);
      const healthText = health.status === "fulfilled" ? health.value.status : "unavailable";
      const runText = latest.status === "fulfilled" && latest.value.status ? latest.value.status : "no run";
      cell.innerHTML = `${pill(healthText, healthText === "ready" ? "ok" : "warn")}<span>${escapeHTML(runText)}</span>`;
    } catch {
      cell.textContent = "unavailable";
    }
  }));
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

function aliases(project) {
  if (!Array.isArray(project.aliases) || project.aliases.length === 0) return "";
  return `<span>${project.aliases.map(escapeHTML).join(", ")}</span>`;
}

function integrations(value) {
  if (!value) return `<span class="muted-text">none</span>`;
  const parts = [];
  if (value.jira) parts.push(provider("Jira", value.jira));
  if (value.confluence) parts.push(provider("Confluence", value.confluence));
  return parts.length ? parts.join("") : `<span class="muted-text">none</span>`;
}

function provider(name, value) {
  const state = value.enabled ? "on" : "off";
  const count = value.project_key_count || value.space_key_count || 0;
  return `<div>${escapeHTML(name)} ${pill(state, value.enabled ? "ok" : "muted")}<span>${count} scopes, ${value.ingestion_enabled ? "ingest on" : "ingest off"}</span></div>`;
}

function pill(text, tone) {
  return `<span class="pill ${tone}">${escapeHTML(text)}</span>`;
}

function escapeHTML(value) {
  return String(value ?? "").replace(/[&<>"']/g, (char) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    "\"": "&quot;",
    "'": "&#39;",
  }[char]));
}

function cssEscape(value) {
  if (window.CSS && CSS.escape) return CSS.escape(value);
  return String(value).replace(/["\\]/g, "\\$&");
}
