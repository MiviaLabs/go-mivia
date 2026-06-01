const projectsBody = document.querySelector("#projects");
const summary = document.querySelector("#summary");
const statusBox = document.querySelector("#status");
const refresh = document.querySelector("#refresh");
const back = document.querySelector("#back");
const metrics = document.querySelector("#metrics");
const list = document.querySelector("#list");
const detail = document.querySelector("#detail");

refresh.addEventListener("click", loadCurrentView);
back.addEventListener("click", () => {
  location.hash = "";
});
window.addEventListener("hashchange", loadCurrentView);

loadCurrentView();

async function loadCurrentView() {
  const projectID = selectedProjectID();
  if (projectID) {
    await loadProjectDetail(projectID);
    return;
  }
  await loadDashboard();
}

async function loadDashboard() {
  refresh.disabled = true;
  back.classList.add("hidden");
  statusBox.textContent = "";
  detail.classList.add("hidden");
  detail.replaceChildren();
  list.classList.remove("hidden");
  metrics.innerHTML = "";
  projectsBody.innerHTML = "";
  summary.textContent = "Loading projects";

  try {
    const data = await fetchJSON("/api/v1/projects", 4000);
    const projects = Array.isArray(data.projects) ? data.projects : [];
    summary.textContent = `${projects.length} configured project${projects.length === 1 ? "" : "s"}`;
    renderMetrics(projects);
    renderProjects(projects);
    await loadOptionalStatus(projects);
  } catch (error) {
    summary.textContent = "Projects unavailable";
    statusBox.textContent = error.message;
  } finally {
    refresh.disabled = false;
  }
}

async function loadProjectDetail(projectID) {
  refresh.disabled = true;
  back.classList.remove("hidden");
  statusBox.textContent = "";
  list.classList.add("hidden");
  metrics.innerHTML = "";
  detail.classList.remove("hidden");
  detail.innerHTML = `<section class="panel loading">Loading project details</section>`;
  summary.textContent = projectID;

  try {
    const dashboard = await fetchJSON(`/api/v1/projects/${encodeURIComponent(projectID)}/dashboard-summary`, 12000);
    const projectData = dashboard.project;
    const healthData = dashboard.context_health;
    const latestData = dashboard.latest_run;
    summary.textContent = projectData.display_name || projectData.id;
    renderProjectDetail(projectData, healthData, latestData, dashboard);
  } catch (error) {
    summary.textContent = "Project unavailable";
    statusBox.textContent = error.message;
    detail.innerHTML = `<section class="panel empty">Project details could not be loaded.</section>`;
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
  const card = document.createElement("article");
  card.className = "metric";
  card.innerHTML = `
    <span>${escapeHTML(label)}</span>
    <strong>${escapeHTML(value)}</strong>
    <small>${escapeHTML(note)}</small>
  `;
  return card;
}

function renderProjects(projects) {
  if (projects.length === 0) {
    projectsBody.innerHTML = `<tr><td colspan="6" class="empty">No projects configured</td></tr>`;
    return;
  }

  projectsBody.replaceChildren(...projects.map((project) => {
    const row = document.createElement("tr");
    row.className = "project-row";
    row.tabIndex = 0;
    row.dataset.projectId = project.id;
    row.addEventListener("click", () => openProject(project.id));
    row.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        openProject(project.id);
      }
    });
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

function renderProjectDetail(project, health, latest, dashboard) {
  const graph = dashboard?.graph || {};
  const fileItems = Array.isArray(graph.files?.sample) ? graph.files.sample : [];
  detail.innerHTML = `
    <aside class="detail-rail">
      ${renderBasicInfoSection(project)}
      ${renderStatsSection(health, graph, dashboard?.workspace)}
      ${renderAliasesSection(project)}
      ${renderControlPlaneSection()}
    </aside>
    <section class="detail-main">
      ${renderHeroSection(project, health)}
      ${renderPipelineSection(project, health, latest, graph, dashboard)}
      ${renderOperationalVisualsSection(project, health, latest, graph, dashboard)}
      ${renderGraphStatsSection(graph)}
      ${renderContextHealthSection(project, health)}
      ${renderLatestIngestionSection(latest)}
      ${renderWorkspaceSection(dashboard?.workspace)}
      ${renderASTCoverageSection(graph.ast_coverage)}
      ${renderRecentFilesSection(fileItems)}
      ${renderIntegrationsSection(project, dashboard?.integrations)}
      ${renderWarningsSection(dashboard?.warnings)}
    </section>
  `;
}

function renderBasicInfoSection(project) {
  return panel("Basic info", infoList([
    ["Name", project.display_name || project.id],
    ["Project ID", project.id],
    ["Enabled", project.enabled ? "yes" : "no"],
    ["Validation", project.validation_status || "unknown"],
    ["Workspace", project.workspace_mode || "unknown"],
    ["Classification", project.classification || "unknown"],
  ]));
}

function renderStatsSection(health, graph, workspace) {
  return panel("Stats", infoList([
    ["Health", health?.status || "unavailable"],
    ["Files", numberValue(health?.eligible_file_count)],
    ["Symbols", numberValue(health?.indexed_symbol_count)],
    ["Chunks", numberValue(health?.indexed_chunk_count)],
    ["Headings", numberValue(graph?.headings?.sampled_count)],
    ["Dirty sampled", numberValue(workspace?.sampled_dirty_count ?? 0)],
    ["Search", health?.search_index?.status || "unknown"],
    ["Git", health?.workspace_git_available ? "available" : "unavailable"],
  ]));
}

function renderAliasesSection(project) {
  return panel("Aliases", aliasesList(project.aliases));
}

function renderControlPlaneSection() {
  return panel("Control plane", infoList([
    ["Task runs", "create/get by ID"],
    ["Research runs", "create/get by ID"],
    ["Agent runs", "create/get by ID"],
    ["Aggregates", "list/count API not exposed"],
  ]));
}

function renderHeroSection(project, health) {
  const status = health?.status || "unavailable";
  return `
    <section class="project-hero panel">
      <div>
        <span class="eyebrow">Project</span>
        <h2>${escapeHTML(project.display_name || project.id)}</h2>
        <p>${escapeHTML(project.description || "No description configured")}</p>
      </div>
      <div class="hero-status">
        ${pill(status, status === "ready" ? "ok" : "warn")}
        ${pill(project.validation_status || "unknown", project.validation_status === "valid" ? "ok" : "warn")}
      </div>
    </section>
  `;
}

function renderPipelineSection(project, health, latest, graph, dashboard) {
  return panel("Data pipeline", projectPipelineDiagram(project, health, latest, graph, dashboard));
}

function renderOperationalVisualsSection(project, health, latest, graph, dashboard) {
  return panel("Operational visuals", `
    <div class="split-grid">
      ${renderIngestionFlowVisual(project, health, latest, graph)}
      ${renderGraphCompositionVisual(health, graph)}
      ${renderSymbolConcentrationVisual(graph?.symbols)}
      ${countBlock("Symbols by language", graph?.symbols?.by_language, { bars: true })}
      ${renderWorkspaceDirtyVisual(dashboard?.workspace)}
      ${renderIntegrationProviderVisual(project, dashboard?.integrations)}
    </div>
    ${renderLatestIngestionTimeline(latest)}
    ${renderASTCoverageMatrix(graph?.ast_coverage)}
  `);
}

function renderGraphStatsSection(graph) {
  return panel("Graph stats", `
    <div class="split-grid">
      ${countBlock("Files by extension", graph?.files?.by_extension, { bars: true })}
      ${countBlock("Symbols by kind", graph?.symbols?.by_kind, { bars: true })}
      ${symbolConcentrationBlock(graph?.symbols)}
      ${countBlock("Symbols by language", graph?.symbols?.by_language, { bars: true })}
      ${countBlock("Headings by level", graph?.headings?.by_level, { bars: true })}
    </div>
  `);
}

function renderContextHealthSection(project, health) {
  return panel("Context health", contextHealth(project, health));
}

function renderLatestIngestionSection(latest) {
  return panel("Latest ingestion", latestRun(latest));
}

function renderWorkspaceSection(workspace) {
  if (!workspace) return panel("Workspace", `<p class="empty">Workspace git status unavailable.</p>`);
  return panel("Workspace", `
    <div class="split-grid">
      ${infoList([
        ["Branch", workspace.branch || "unknown"],
        ["Head", workspace.head_oid_short || "unknown"],
        ["Dirty sampled", numberValue(workspace.sampled_dirty_count)],
        ["Truncated", workspace.truncated ? "yes" : "no"],
      ])}
      ${countBlock("Status", workspace.by_status)}
    </div>
    ${gitSample(workspace.sample)}
  `);
}

function renderASTCoverageSection(coverage) {
  if (!Array.isArray(coverage) || coverage.length === 0) return panel("AST coverage", `<p class="empty">AST coverage unavailable.</p>`);
  return panel("AST coverage", `
    <div class="coverage-grid">
      ${coverage.map((item) => `
        <div class="coverage-row">
          <strong>${escapeHTML(item.language || "unknown")}</strong>
          <span>${escapeHTML(item.coverage_status || "unknown")}</span>
          <small>${numberValue(item.eligible_files)} files / ${escapeHTML((item.extensions || []).join(", ") || "no extensions")}</small>
        </div>
      `).join("")}
    </div>
  `);
}

function renderRecentFilesSection(files) {
  return panel("Recent files", recentFiles(files));
}

function renderIntegrationsSection(project, integrationSummary) {
  const providerStatus = Array.isArray(integrationSummary?.providers) ? integrationSummary.providers : [];
  const counts = Array.isArray(integrationSummary?.counts) ? integrationSummary.counts : [];
  return panel("Integrations", `
    ${integrations(project.integrations)}
    ${providerStatus.length ? `<div class="integration-grid">${providerStatus.map((provider) => `
      <div class="integration-row">
        <strong>${escapeHTML(provider.provider)}</strong>
        <span>${provider.enabled ? "enabled" : "disabled"} / ${provider.ingestion_enabled ? "ingest on" : "ingest off"}</span>
        <small>${escapeHTML(provider.allowlist_kind || "allowlist")} ${numberValue(provider.allowlist_count)} scopes, source ${provider.source_persisted ? "persisted" : "not persisted"}</small>
      </div>
    `).join("")}</div>` : `<p class="empty">No live integration status returned.</p>`}
    ${counts.length ? countBlock("Indexed integration items", counts.map((item) => ({ key: item.provider, count: item.count })), { bars: true }) : ""}
  `);
}

function renderWarningsSection(warnings) {
  if (!Array.isArray(warnings) || warnings.length === 0) return "";
  return panel("Partial data", `<div class="tag-list">${warnings.map((warning) => `<span>${escapeHTML(warning)}</span>`).join("")}</div>`);
}

function renderIngestionFlowVisual(project, health, latest, graph) {
  const seen = latest?.files_seen ?? graph?.files?.sampled_count ?? 0;
  const ingested = latest?.files_ingested ?? 0;
  const unchanged = latest?.files_unchanged ?? 0;
  const skipped = latest?.files_skipped ?? 0;
  const chunks = latest?.chunks_stored ?? health?.indexed_chunk_count ?? 0;
  const symbols = latest?.symbols_stored ?? health?.indexed_symbol_count ?? 0;
  const stages = [
    ["Config", project.enabled ? "enabled" : "disabled", project.enabled ? "ok" : "warn"],
    ["Scan", `${numberValue(seen)} seen`, seen > 0 ? "ok" : "muted"],
    ["Store", `${numberValue(chunks)} chunks`, chunks > 0 ? "ok" : "muted"],
    ["Index", `${numberValue(symbols)} symbols`, symbols > 0 ? "ok" : "muted"],
    ["Serve", health?.status || "unknown", health?.indexed_content_available ? "ok" : "warn"],
  ];
  return `
    <div>
      <h3>Ingestion flow</h3>
      <div class="diagram">
        ${stages.map(([label, value, tone]) => `
          <div class="diagram-node">
            ${pill(label, tone)}
            <strong>${escapeHTML(value)}</strong>
            <span>${escapeHTML(label === "Scan" ? `${numberValue(ingested)} new, ${numberValue(unchanged)} unchanged, ${numberValue(skipped)} skipped` : "")}</span>
          </div>
        `).join("")}
      </div>
    </div>
  `;
}

function renderGraphCompositionVisual(health, graph) {
  const items = [
    { key: "Files", count: health?.eligible_file_count ?? graph?.files?.sampled_count },
    { key: "Chunks", count: health?.indexed_chunk_count },
    { key: "Symbols", count: health?.indexed_symbol_count },
    { key: "Headings", count: graph?.headings?.sampled_count },
  ];
  return `
    <div>
      <h3>Graph composition</h3>
      ${donutVisual(items)}
      ${distributionBars(items)}
    </div>
  `;
}

function renderSymbolConcentrationVisual(symbols) {
  const source = symbolConcentrationSource(symbols);
  const values = normalizeCounts(source.items).slice(0, 8);
  if (!values.length) return `<div><h3>Symbol concentration</h3><p class="empty">No concentration data.</p></div>`;
  const displayedTotal = values.reduce((sum, item) => sum + item.count, 0);
  const denominatorCount = Number.isFinite(source.denominatorCount) && source.denominatorCount > 0 ? source.denominatorCount : displayedTotal;
  const leader = values[0];
  const denominator = source.denominatorLabel || `sample of ${numberValue(denominatorCount)} symbols`;
  const scopeNote = symbols?.sample_truncated ? "Full indexed graph" : "Current indexed graph";
  return `
    <div>
      <h3>${escapeHTML(source.title)}</h3>
      <div class="metric" style="min-height:auto; margin-bottom:8px; box-shadow:none;">
        <span>${escapeHTML(source.leaderLabel)} · ${escapeHTML(scopeNote)}</span>
        <strong>${percent(leader.count, denominatorCount)}</strong>
        <small>${escapeHTML(leader.key)} / ${escapeHTML(denominator)}</small>
      </div>
      ${distributionBars(values, { total: denominatorCount })}
    </div>
  `;
}

function symbolConcentrationSource(symbols) {
  if (!symbols || typeof symbols !== "object") {
    return {
      title: "Symbol concentration",
      leaderLabel: "Top code area share",
      items: [],
      denominatorLabel: "0 indexed symbols",
    };
  }
  const basis = symbols.concentration_basis || {};
  const sources = symbolConcentrationCandidates(symbols, basis);
  for (const source of sources) {
    if (source.enabled && normalizeCounts(source.items).length) {
      const basisDenominator = Number.isFinite(basis.denominator_count) && basis.denominator_count > 0 ? basis.denominator_count : 0;
      return {
        title: source.title,
        leaderLabel: source.leaderLabel,
        items: source.items,
        denominatorLabel: source.denominatorLabel || `${numberValue(basisDenominator || symbols.total_count || symbols.sampled_count)} indexed symbols`,
        denominatorCount: basisDenominator || symbols.total_count || symbols.sampled_count,
      };
    }
  }
  return {
    title: "Symbol concentration",
    leaderLabel: "Top code area share",
    items: [],
    denominatorLabel: "0 indexed symbols",
  };
}

function symbolConcentrationCandidates(symbols, basis) {
  const preferred = {
    by_module: {
      enabled: basis.source !== "relative_path_bucket",
      items: symbols.by_module,
      title: "Module concentration",
      leaderLabel: "Top module share",
      denominatorLabel: `${numberValue(basis.denominator_count)} indexed JS/TS symbols`,
    },
    by_namespace: {
      enabled: true,
      items: symbols.by_namespace,
      title: "Namespace concentration",
      leaderLabel: "Top namespace share",
      denominatorLabel: `${numberValue(basis.denominator_count)} indexed C# namespace symbols`,
    },
    by_assembly: {
      enabled: true,
      items: symbols.by_assembly,
      title: "Assembly concentration",
      leaderLabel: "Top assembly share",
      denominatorLabel: `${numberValue(basis.denominator_count)} indexed Unity C# symbols`,
    },
    by_package: {
      enabled: true,
      items: symbols.by_package,
      title: "Package concentration",
      leaderLabel: "Top package share",
      denominatorLabel: `${numberValue(basis.denominator_count)} indexed package symbols`,
    },
    by_code_area: {
      enabled: true,
      items: symbols.by_code_area || symbols.by_path_bucket || symbols.by_directory || symbols.by_path,
      title: "Code area concentration",
      leaderLabel: "Top code area share",
      denominatorLabel: `${numberValue(basis.denominator_count || symbols.total_count)} indexed symbols`,
    },
  };
  const orderedKeys = ["by_module", "by_namespace", "by_assembly", "by_package", "by_code_area"];
  const primary = preferred[basis.primary_field];
  return primary ? [primary, ...orderedKeys.filter((key) => key !== basis.primary_field).map((key) => preferred[key])] : orderedKeys.map((key) => preferred[key]);
}

function symbolConcentrationBlock(symbols) {
  const source = symbolConcentrationSource(symbols);
  return countBlock(source.title, source.items, { bars: true, total: source.denominatorCount });
}

function renderWorkspaceDirtyVisual(workspace) {
  if (!workspace) return `<div><h3>Workspace dirty summary</h3><p class="empty">Workspace git status unavailable.</p></div>`;
  const values = normalizeCounts(workspace.by_status);
  const dirty = workspace.sampled_dirty_count ?? values.reduce((sum, item) => sum + item.count, 0);
  return `
    <div>
      <h3>Workspace dirty summary</h3>
      <div class="diagram">
        <div class="diagram-node">
          ${pill(dirty > 0 ? "dirty" : "clean", dirty > 0 ? "warn" : "ok")}
          <strong>${numberValue(dirty)} sampled path${dirty === 1 ? "" : "s"}</strong>
          <span>${escapeHTML(workspace.branch || "unknown branch")} @ ${escapeHTML(workspace.head_oid_short || "unknown")}</span>
        </div>
        <div class="diagram-node">
          ${pill(workspace.truncated ? "truncated" : "complete", workspace.truncated ? "warn" : "ok")}
          <strong>${values.length ? `${numberValue(values.length)} statuses` : "No changes"}</strong>
          <span>Sample limit only; no raw diffs rendered.</span>
        </div>
      </div>
      ${values.length ? distributionBars(values) : ""}
    </div>
  `;
}

function renderIntegrationProviderVisual(project, integrationSummary) {
  const providers = Array.isArray(integrationSummary?.providers) ? integrationSummary.providers : [];
  const counts = Array.isArray(integrationSummary?.counts) ? integrationSummary.counts : [];
  const configured = providers.length ? providers : projectProviders(project.integrations);
  if (!configured.length && !counts.length) return `<div><h3>Integration providers</h3><p class="empty">No providers configured.</p></div>`;
  return `
    <div>
      <h3>Integration providers</h3>
      <div class="integration-grid">
        ${configured.map((provider) => providerVisual(provider, counts)).join("")}
      </div>
      ${counts.length ? distributionBars(counts.map((item) => ({ key: item.provider, count: item.count }))) : ""}
    </div>
  `;
}

function providerVisual(providerData, counts) {
  const providerName = providerData.provider || providerData.name || "unknown";
  const count = counts.find((item) => item.provider === providerName)?.count;
  const enabled = Boolean(providerData.enabled);
  const ingest = Boolean(providerData.ingestion_enabled);
  const scopes = providerData.allowlist_count ?? providerData.project_key_count ?? providerData.space_key_count ?? 0;
  return `
    <div class="integration-row">
      <strong>${escapeHTML(providerName)}</strong>
      <span>${pill(enabled ? "enabled" : "disabled", enabled ? "ok" : "muted")} ${pill(ingest ? "ingest on" : "ingest off", ingest ? "ok" : "muted")}</span>
      <small>${numberValue(scopes)} configured scopes${typeof count === "number" ? `, ${numberValue(count)} local items` : ""}</small>
    </div>
  `;
}

function renderLatestIngestionTimeline(run) {
  if (!run) return `<p class="empty">Latest ingestion timeline unavailable.</p>`;
  const points = [
    ["Started", run.started_at],
    ["Progress", run.last_progress_at || run.heartbeat_at],
    ["Heartbeat", run.heartbeat_at],
    ["Finished", run.finished_at],
  ].filter(([, value]) => value);
  if (!points.length) return `<p class="empty">Latest ingestion timestamps unavailable.</p>`;
  return `
    <div>
      <h3>Latest ingestion timeline</h3>
      <div class="timeline">
        ${points.map(([label, value]) => `
          <div class="timeline-item">
            <strong>${escapeHTML(label)}</strong>
            <span>${formatDate(value)}</span>
          </div>
        `).join("")}
      </div>
    </div>
  `;
}

function renderASTCoverageMatrix(coverage) {
  if (!Array.isArray(coverage) || coverage.length === 0) return `<p class="empty">AST coverage unavailable.</p>`;
  return `
    <div>
      <h3>AST coverage matrix</h3>
      <div class="coverage-grid">
        ${coverage.map((item) => {
          const eligible = item.eligible_files ?? 0;
          const skipped = item.skipped_file_too_large ?? 0;
          const complete = item.coverage_status === "complete";
          return `
            <div class="coverage-row">
              <strong>${escapeHTML(item.language || "unknown")}</strong>
              <span>${pill(item.coverage_status || "unknown", complete ? "ok" : "warn")} ${numberValue(eligible)} eligible</span>
              <small>${escapeHTML((item.extensions || []).join(", ") || "no extensions")} / ${numberValue(skipped)} oversized skips</small>
            </div>
          `;
        }).join("")}
      </div>
    </div>
  `;
}

function projectPipelineDiagram(project, health, latest, graph, dashboard) {
  const nodes = [
    {
      title: "Project config",
      detail: `${project.enabled ? "enabled" : "disabled"} / ${project.validation_status || "unknown"}`,
      tone: project.enabled && project.validation_status === "valid" ? "ok" : "warn",
    },
    {
      title: "Scan",
      detail: latest?.status || health?.latest_run?.status || "unavailable",
      tone: latest?.status === "completed" || health?.latest_run?.status === "completed" ? "ok" : "warn",
    },
    {
      title: "Chunks",
      detail: numberValue(health?.indexed_chunk_count),
      tone: health?.indexed_chunk_count > 0 ? "ok" : "muted",
    },
    {
      title: "Symbols",
      detail: `${numberValue(health?.indexed_symbol_count)} symbols`,
      tone: health?.indexed_symbol_count > 0 ? "ok" : "muted",
    },
    {
      title: "Graph stats",
      detail: `${numberValue(graph?.files?.sampled_count)} files sampled`,
      tone: graph?.files?.sampled_count > 0 ? "ok" : "muted",
    },
    {
      title: "Search index",
      detail: graph?.search_index?.status || health?.search_index?.status || "unknown",
      tone: (graph?.search_index?.status || health?.search_index?.status) === "ok" ? "ok" : "warn",
    },
    {
      title: "REST summary",
      detail: `${numberValue(dashboard?.limits?.files_page_size)} file sample`,
      tone: health?.indexed_content_available ? "ok" : "warn",
    },
  ];
  const width = 1220;
  const height = 230;
  const boxWidth = 146;
  const boxHeight = 74;
  const startX = 22;
  const gap = 16;
  const y = 74;
  const statusText = contextStatusLabel(health);

  return `
    <div class="pipeline-diagram" role="img" aria-label="${escapeHTML(statusText)} data pipeline">
      <svg viewBox="0 0 ${width} ${height}" width="100%" height="230" xmlns="http://www.w3.org/2000/svg">
        <defs>
          <marker id="pipeline-arrow" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">
            <path d="M0,0 L8,4 L0,8 Z" fill="currentColor"></path>
          </marker>
        </defs>
        <text x="${startX}" y="30" fill="currentColor" font-size="15" font-weight="700">${escapeHTML(statusText)}</text>
        <text x="${startX}" y="52" fill="currentColor" opacity="0.68" font-size="12">One bounded REST summary: graph metadata, AST coverage, git status, integration status. No source text or diffs.</text>
        ${nodes.map((node, index) => pipelineNode(node, startX + index * (boxWidth + gap), y, boxWidth, boxHeight)).join("")}
        ${nodes.slice(0, -1).map((_, index) => pipelineArrow(startX + index * (boxWidth + gap) + boxWidth, y + boxHeight / 2, startX + (index + 1) * (boxWidth + gap), y + boxHeight / 2)).join("")}
      </svg>
    </div>
  `;
}

function pipelineNode(node, x, y, width, height) {
  const tone = node.tone === "ok" ? "var(--ok)" : node.tone === "warn" ? "var(--warn)" : "var(--muted)";
  return `
    <g>
      <rect x="${x}" y="${y}" width="${width}" height="${height}" rx="8" fill="var(--panel-soft)" stroke="${tone}" stroke-width="1.5"></rect>
      <circle cx="${x + 16}" cy="${y + 18}" r="5" fill="${tone}"></circle>
      <text x="${x + 28}" y="${y + 23}" fill="currentColor" font-size="12" font-weight="700">${escapeHTML(node.title)}</text>
      <text x="${x + 14}" y="${y + 52}" fill="currentColor" opacity="0.72" font-size="11">${escapeHTML(node.detail)}</text>
    </g>
  `;
}

function pipelineArrow(x1, y1, x2, y2) {
  return `<line x1="${x1 + 6}" y1="${y1}" x2="${x2 - 6}" y2="${y2}" stroke="currentColor" stroke-width="1.5" opacity="0.5" marker-end="url(#pipeline-arrow)"></line>`;
}

function contextHealth(project, health) {
  if (!health) return `<p class="empty">Context health unavailable.</p>`;
  const reasonCounts = Object.entries(health.reason_counts || {});
  return `
    <div class="split-grid">
      ${infoList([
        ["Status", contextStatusLabel(health)],
        ["Reason", health.status_reason || "none"],
        ["Digest mode", project.digest_mode || "unknown"],
        ["Update policy", project.update_policy || "unknown"],
        ["Indexed content", health.indexed_content_available ? "available" : "unavailable"],
        ["Last checked", formatDate(health.checked_at)],
      ])}
      <div>
        <h3>Skipped reasons</h3>
        ${reasonCounts.length ? reasonCounts.map(([key, value]) => `<div class="reason-row"><span>${escapeHTML(key)}</span><strong>${escapeHTML(value)}</strong></div>`).join("") : `<p class="empty">No skipped reason counts.</p>`}
      </div>
    </div>
  `;
}

function contextStatusLabel(health) {
  if (!health?.status) return "Context unavailable";
  if (health.status === "ready" && health.status_reason === "file_warnings") {
    return "ready with warnings";
  }
  return health.status;
}

function latestRun(run) {
  if (!run) return `<p class="empty">Latest ingestion run unavailable.</p>`;
  return `
    <div class="split-grid">
      ${infoList([
        ["Run ID", run.id || "unknown"],
        ["Status", run.status || "unknown"],
        ["Trigger", run.trigger || "unknown"],
        ["Mode", run.mode || "unknown"],
        ["Phase", run.current_phase || "unknown"],
        ["Started", formatDate(run.started_at)],
        ["Finished", formatDate(run.finished_at)],
      ])}
      ${infoList([
        ["Files seen", numberValue(run.files_seen)],
        ["Ingested", numberValue(run.files_ingested)],
        ["Skipped", numberValue(run.files_skipped)],
        ["Unchanged", numberValue(run.files_unchanged)],
        ["Chunks", numberValue(run.chunks_stored)],
        ["Symbols", numberValue(run.symbols_stored)],
      ])}
    </div>
  `;
}

function recentFiles(files) {
  if (files.length === 0) return `<p class="empty">No files returned.</p>`;
  return `
    <div class="file-list">
      ${files.map((file) => `
        <div class="file-row">
          <strong>${escapeHTML(file.relative_path || file.id)}</strong>
          <span>${escapeHTML(file.status || "unknown")} / ${escapeHTML(file.extension || "no extension")} / ${file.present ? "present" : "absent"}</span>
        </div>
      `).join("")}
    </div>
  `;
}

function countBlock(title, items, options = {}) {
  if (!Array.isArray(items) || items.length === 0) return `<div><h3>${escapeHTML(title)}</h3><p class="empty">No data.</p></div>`;
  if (options.bars) {
    return `
      <div>
        <h3>${escapeHTML(title)}</h3>
        ${distributionBars(items, { total: options.total })}
      </div>
    `;
  }
  return `
    <div>
      <h3>${escapeHTML(title)}</h3>
      ${items.map((item) => `<div class="reason-row"><span>${escapeHTML(item.key || item.provider || "unknown")}</span><strong>${numberValue(item.count)}</strong></div>`).join("")}
    </div>
  `;
}

function distributionBars(items, options = {}) {
  const values = normalizeCounts(items);
  if (!values.length) return `<p class="empty">No data.</p>`;
  const max = Math.max(...values.map((item) => item.count), 1);
  const total = Number.isFinite(options.total) && options.total > 0 ? options.total : 0;
  return `
    <div class="content-list">
      ${values.map((item) => {
        const width = Math.max(4, Math.round((item.count / max) * 100));
        const countLabel = total ? `${numberValue(item.count)} · ${percent(item.count, total)}` : numberValue(item.count);
        return `
          <div class="reason-row">
            <span>${escapeHTML(item.key)}</span>
            <strong>${escapeHTML(countLabel)}</strong>
            <span style="grid-column:1 / -1; display:block; height:6px; margin-top:2px; border-radius:999px; background:linear-gradient(90deg, var(--accent), color-mix(in srgb, var(--node) 58%, var(--accent))) left / ${width}% 100% no-repeat, var(--bar-track);"></span>
          </div>
        `;
      }).join("")}
    </div>
  `;
}

function donutVisual(items) {
  const values = normalizeCounts(items);
  const total = values.reduce((sum, item) => sum + item.count, 0);
  if (!total) return `<p class="empty">No graph totals.</p>`;
  let offset = 25;
  const colors = ["var(--accent)", "var(--node)", "var(--ok)", "var(--warn)"];
  const rings = values.map((item, index) => {
    const length = (item.count / total) * 100;
    const stroke = colors[index % colors.length];
    const circle = `<circle cx="58" cy="58" r="46" fill="none" stroke="${stroke}" stroke-width="14" stroke-dasharray="${length} ${100 - length}" stroke-dashoffset="${offset}" pathLength="100"></circle>`;
    offset -= length;
    return circle;
  }).join("");
  return `
    <div class="diagram-node" style="display:grid; grid-template-columns:auto minmax(0,1fr); gap:12px; align-items:center; margin-bottom:8px;">
      <svg viewBox="0 0 116 116" width="116" height="116" role="img" aria-label="Graph composition donut">
        <circle cx="58" cy="58" r="46" fill="none" stroke="var(--bar-track)" stroke-width="14"></circle>
        ${rings}
        <text x="58" y="55" text-anchor="middle" fill="currentColor" font-size="15" font-weight="700">${numberValue(total)}</text>
        <text x="58" y="72" text-anchor="middle" fill="currentColor" opacity="0.64" font-size="10">total</text>
      </svg>
      <div>${values.map((item) => `<span>${escapeHTML(item.key)}: ${numberValue(item.count)}</span>`).join("")}</div>
    </div>
  `;
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

function projectProviders(integrationsValue) {
  if (!integrationsValue) return [];
  const providers = [];
  if (integrationsValue.jira) providers.push({ provider: "jira", ...integrationsValue.jira });
  if (integrationsValue.confluence) providers.push({ provider: "confluence", ...integrationsValue.confluence });
  return providers;
}

function percent(value, total) {
  if (!total) return "0%";
  return `${Math.round((value / total) * 100)}%`;
}

function gitSample(items) {
  if (!Array.isArray(items) || items.length === 0) return `<p class="empty">No working tree changes.</p>`;
  return `
    <div class="file-list">
      ${items.map((item) => `
        <div class="file-row">
          <strong>${escapeHTML(item.relative_path)}</strong>
          <span>${escapeHTML(item.status || "unknown")}</span>
        </div>
      `).join("")}
    </div>
  `;
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

function openProject(id) {
  location.hash = `project=${encodeURIComponent(id)}`;
}

function selectedProjectID() {
  const hash = location.hash.replace(/^#/, "");
  if (!hash) return "";
  const params = new URLSearchParams(hash);
  return params.get("project") || "";
}

function panel(title, body) {
  return `
    <section class="panel">
      <h3>${escapeHTML(title)}</h3>
      ${body}
    </section>
  `;
}

function infoList(items) {
  return `
    <dl class="info-list">
      ${items.map(([label, value]) => `
        <div>
          <dt>${escapeHTML(label)}</dt>
          <dd>${escapeHTML(value)}</dd>
        </div>
      `).join("")}
    </dl>
  `;
}

function aliases(project) {
  if (!Array.isArray(project.aliases) || project.aliases.length === 0) return "";
  return `<span>${project.aliases.map(escapeHTML).join(", ")}</span>`;
}

function aliasesList(values) {
  if (!Array.isArray(values) || values.length === 0) return `<p class="empty">No aliases configured.</p>`;
  return `<div class="tag-list">${values.map((value) => `<span>${escapeHTML(value)}</span>`).join("")}</div>`;
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
