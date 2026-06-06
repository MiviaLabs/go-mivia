package httpapi

import (
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

type Options struct {
	Proxy     http.Handler
	StaticDir string
}

func RegisterRoutes(mux *http.ServeMux, options Options) {
	static := securityHeaders(staticHandler{dir: strings.TrimSpace(options.StaticDir)})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
	})
	mux.HandleFunc("GET /dashboard", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard/", http.StatusFound)
	})
	mux.Handle("GET /dashboard/", http.StripPrefix("/dashboard/", static))
	if options.Proxy != nil {
		mux.Handle("/api/v1/", options.Proxy)
	}
}

type staticHandler struct {
	dir string
}

func (handler staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := cleanAssetName(r.URL.Path)
	if name == "" {
		http.NotFound(w, r)
		return
	}
	if name == "." || name == "/" {
		name = "index.html"
	}
	if handler.serveExternal(w, r, name) {
		return
	}
	if content, ok := fallbackAssets[name]; ok {
		serveString(w, r, name, content)
		return
	}
	if path.Ext(name) == "" {
		serveString(w, r, "index.html", fallbackAssets["index.html"])
		return
	}
	http.NotFound(w, r)
}

func (handler staticHandler) serveExternal(w http.ResponseWriter, r *http.Request, name string) bool {
	if handler.dir == "" {
		return false
	}
	file, err := os.Open(path.Join(handler.dir, name))
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		return false
	}
	serveReadSeeker(w, r, name, info.ModTime(), file)
	return true
}

func cleanAssetName(raw string) string {
	if raw == "" {
		return "index.html"
	}
	trimmed := strings.TrimPrefix(raw, "/")
	for _, segment := range strings.Split(trimmed, "/") {
		if segment == ".." {
			return ""
		}
	}
	cleaned := path.Clean("/" + trimmed)
	if cleaned == "/" {
		return "index.html"
	}
	name := strings.TrimPrefix(cleaned, "/")
	if name == "" || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") || strings.ContainsAny(name, "\x00\r\n") {
		return ""
	}
	return name
}

func serveString(w http.ResponseWriter, r *http.Request, name string, content string) {
	serveReadSeeker(w, r, name, time.Time{}, strings.NewReader(content))
}

func serveReadSeeker(w http.ResponseWriter, r *http.Request, name string, modTime time.Time, content io.ReadSeeker) {
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	http.ServeContent(w, r, name, modTime, content)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", strings.Join([]string{
			"default-src 'self'",
			"script-src 'self'",
			"style-src 'self'",
			"connect-src 'self'",
			"img-src 'self'",
			"base-uri 'none'",
			"form-action 'none'",
			"frame-ancestors 'none'",
		}, "; "))
		next.ServeHTTP(w, r)
	})
}

var fallbackAssets = map[string]string{
	"index.html": fallbackIndex,
	"app.js":     fallbackAppJS,
	"styles.css": fallbackStyles,
}

const fallbackIndex = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Mivia Dashboard</title>
  <link rel="stylesheet" href="./styles.css">
</head>
<body>
  <header class="topbar">
    <h1>Mivia Dashboard</h1>
    <span class="mode">Read-only cockpit</span>
  </header>
  <main id="app" class="layout" aria-live="polite"></main>
  <script src="./app.js"></script>
</body>
</html>
`

const fallbackAppJS = `"use strict";

const app = document.querySelector("#app");
let activitySource = null;

function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs)) {
    if (key === "class") node.className = value;
    else if (key === "text") node.textContent = value;
    else if (key.startsWith("on") && typeof value === "function") node.addEventListener(key.slice(2).toLowerCase(), value);
    else node.setAttribute(key, value);
  }
  for (const child of children) node.append(child);
  return node;
}

async function fetchJSON(url) {
  const response = await fetch(url, { headers: { "Accept": "application/json" } });
  if (!response.ok) throw new Error("request failed");
  return response.json();
}

function projectTitle(project) {
  return project.display_name || project.id || "Project";
}

async function loadProjects() {
  app.replaceChildren(el("p", { class: "muted", text: "Loading projects..." }));
  try {
    const data = await fetchJSON("/api/v1/projects");
    renderProjects(data.projects || []);
  } catch {
    app.replaceChildren(el("p", { class: "error", text: "Project metadata is unavailable." }));
  }
}

function renderProjects(projects) {
  const list = el("div", { class: "project-list" });
  for (const project of projects) {
    list.append(el("button", { class: "project-row", onClick: () => loadProject(project.id) },
      el("span", { text: projectTitle(project) }),
      el("span", { class: "pill", text: project.validation_status || project.classification || "metadata" })
    ));
  }
  app.replaceChildren(
    el("section", { class: "panel" },
      el("h2", { text: "Projects" }),
      projects.length ? list : el("p", { class: "muted", text: "No configured projects." })
    )
  );
}

async function loadProject(projectID) {
  stopActivity();
  app.replaceChildren(el("p", { class: "muted", text: "Loading project..." }));
  try {
    const [detail, summary, health, latest] = await Promise.all([
      fetchJSON("/api/v1/projects/" + encodeURIComponent(projectID)),
      fetchJSON("/api/v1/projects/" + encodeURIComponent(projectID) + "/dashboard-summary"),
      fetchJSON("/api/v1/projects/" + encodeURIComponent(projectID) + "/context-health"),
      fetchJSON("/api/v1/projects/" + encodeURIComponent(projectID) + "/ingestion-runs/latest")
    ]);
    renderProject(projectID, detail, summary, health, latest);
    startActivity(projectID);
  } catch {
    app.replaceChildren(el("p", { class: "error", text: "Project detail is unavailable." }));
  }
}

function renderProject(projectID, detail, summary, health, latest) {
  const project = summary.project || detail;
  const graph = summary.graph || {};
  app.replaceChildren(
    el("section", { class: "panel" },
      el("button", { class: "link-button", onClick: loadProjects, text: "Back to projects" }),
      el("h2", { text: projectTitle(project) }),
      el("div", { class: "stats" },
        stat("Context", health.status || "unknown"),
        stat("Latest run", latest.status || latest.error?.code || "unknown"),
        stat("Files", graph.files?.total_count ?? "0"),
        stat("Symbols", graph.symbols?.total_count ?? "0")
      ),
      el("h3", { text: "Recent activity" }),
      el("div", { id: "activity", class: "activity" }, el("p", { class: "muted", text: "Waiting for activity..." }))
    )
  );
}

function stat(label, value) {
  return el("div", { class: "stat" }, el("span", { text: label }), el("strong", { text: String(value) }));
}

function startActivity(projectID) {
  const target = document.querySelector("#activity");
  if (!target) return;
  activitySource = new EventSource("/api/v1/projects/" + encodeURIComponent(projectID) + "/agent-activity/stream?recent=20");
  activitySource.onmessage = (event) => appendActivity(target, event.data);
  activitySource.addEventListener("mcp_activity", (event) => appendActivity(target, event.data));
}

function appendActivity(target, raw) {
  let summary = "activity";
  try {
    const event = JSON.parse(raw);
    summary = [event.event_kind, event.method || event.tool_name, event.status].filter(Boolean).join(" · ");
  } catch {}
  if (target.firstElementChild?.classList.contains("muted")) target.replaceChildren();
  target.prepend(el("div", { class: "activity-row", text: summary.slice(0, 180) }));
}

function stopActivity() {
  if (activitySource) activitySource.close();
  activitySource = null;
}

loadProjects();
`

const fallbackStyles = `:root {
  color-scheme: light;
  font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  background: #f6f7f9;
  color: #172026;
}

body {
  margin: 0;
}

.topbar {
  align-items: center;
  background: #ffffff;
  border-bottom: 1px solid #d9dee5;
  display: flex;
  gap: 16px;
  justify-content: space-between;
  padding: 14px 24px;
}

h1, h2, h3, p {
  margin: 0;
}

h1 {
  font-size: 20px;
  font-weight: 700;
}

h2 {
  font-size: 18px;
}

h3 {
  font-size: 14px;
  margin-top: 18px;
}

.mode, .pill {
  background: #e9f4ef;
  border: 1px solid #b8d9cb;
  border-radius: 6px;
  color: #1e6548;
  font-size: 12px;
  padding: 4px 8px;
}

.layout {
  margin: 0 auto;
  max-width: 1100px;
  padding: 24px;
}

.panel {
  background: #ffffff;
  border: 1px solid #d9dee5;
  border-radius: 8px;
  padding: 18px;
}

.project-list {
  display: grid;
  gap: 8px;
  margin-top: 16px;
}

.project-row, .link-button {
  background: #ffffff;
  border: 1px solid #c8d0da;
  border-radius: 6px;
  color: #172026;
  cursor: pointer;
  font: inherit;
}

.project-row {
  align-items: center;
  display: flex;
  justify-content: space-between;
  min-height: 44px;
  padding: 10px 12px;
  text-align: left;
}

.link-button {
  margin-bottom: 14px;
  padding: 7px 10px;
}

.stats {
  display: grid;
  gap: 10px;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  margin-top: 16px;
}

.stat {
  border: 1px solid #d9dee5;
  border-radius: 6px;
  display: grid;
  gap: 6px;
  padding: 12px;
}

.stat span, .muted {
  color: #687381;
}

.activity {
  border-top: 1px solid #e2e6eb;
  display: grid;
  gap: 8px;
  margin-top: 10px;
  padding-top: 10px;
}

.activity-row {
  border: 1px solid #d9dee5;
  border-radius: 6px;
  padding: 8px 10px;
}

.error {
  color: #9a3412;
}
`
