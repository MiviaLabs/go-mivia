package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/platform/health"
	"github.com/MiviaLabs/go-mivia/internal/platform/httpserver"
)

type Proxy struct {
	upstream *url.URL
	client   *http.Client
}

func New(upstream *url.URL) *Proxy {
	return NewWithClient(upstream, &http.Client{
		Transport:     http.DefaultTransport,
		CheckRedirect: noRedirect,
	})
}

func NewWithClient(upstream *url.URL, client *http.Client) *Proxy {
	copied := *upstream
	if client == nil {
		client = &http.Client{Transport: http.DefaultTransport, CheckRedirect: noRedirect}
	}
	return &Proxy{upstream: &copied, client: client}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		httpserver.WriteError(w, http.StatusMethodNotAllowed, "dashboard_read_only", "dashboard proxy is read-only")
		return
	}
	if !AllowedReadOnlyPath(r.URL.Path) {
		httpserver.WriteError(w, http.StatusForbidden, "dashboard_route_forbidden", "dashboard proxy route is outside first-release read-only scope")
		return
	}

	out, err := http.NewRequestWithContext(r.Context(), r.Method, p.upstreamURL(r.URL), nil)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadGateway, "upstream_request_failed", "dashboard upstream request could not be built")
		return
	}
	copySafeRequestHeaders(out.Header, r.Header)

	res, err := p.client.Do(out)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadGateway, "upstream_unavailable", "dashboard upstream is unavailable")
		return
	}
	defer res.Body.Close()

	copyResponseHeaders(w.Header(), res.Header)
	w.WriteHeader(res.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	if isSSE(res.Header) {
		copyStreaming(w, res.Body)
		return
	}
	_, _ = io.Copy(w, res.Body)
}

func (p *Proxy) upstreamURL(in *url.URL) string {
	out := *p.upstream
	basePath := strings.TrimRight(out.EscapedPath(), "/")
	requestPath := in.EscapedPath()
	if requestPath == "" {
		requestPath = in.Path
	}
	out.Path = basePath + "/" + strings.TrimLeft(requestPath, "/")
	out.RawPath = ""
	out.RawQuery = in.RawQuery
	return out.String()
}

func ReadyCheck(upstream *url.URL) func(context.Context) error {
	copied := *upstream
	return func(ctx context.Context) error {
		readyURL := copied
		readyURL.Path = path.Join(strings.TrimRight(copied.Path, "/"), "/readyz")
		readyURL.RawPath = ""
		readyURL.RawQuery = ""
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, readyURL.String(), nil)
		if err != nil {
			return err
		}
		res, err := (&http.Client{Transport: http.DefaultTransport, CheckRedirect: noRedirect}).Do(req)
		if err != nil {
			return health.DependencyUnavailable("upstream_unavailable")
		}
		defer res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return health.DependencyUnavailable("upstream_not_ready")
		}
		return nil
	}
}

func AllowedReadOnlyPath(rawPath string) bool {
	segments := pathSegments(rawPath)
	if len(segments) == 0 {
		return false
	}
	if len(segments) == 1 && (segments[0] == "healthz" || segments[0] == "readyz") {
		return true
	}
	if len(segments) >= 5 && segments[0] == "api" && segments[1] == "v1" && segments[2] == "orgs" && segmentSafe(segments[3]) && segments[4] == "knowledge" {
		return len(segments) == 5
	}
	if len(segments) < 3 || segments[0] != "api" || segments[1] != "v1" || segments[2] != "projects" {
		return false
	}
	if len(segments) == 3 {
		return true
	}
	if !segmentSafe(segments[3]) {
		return false
	}
	if len(segments) == 4 {
		return true
	}

	switch segments[4] {
	case "dashboard-summary", "context-health":
		return len(segments) == 5
	case "agent-activity":
		return len(segments) == 6 && segments[5] == "stream"
	case "ingestion-runs":
		return len(segments) == 6 && segmentSafe(segments[5])
	case "work-plans":
		return allowedWorkPlans(segments)
	case "work-tasks":
		return allowedWorkTasks(segments)
	case "automations", "automation-runs":
		return len(segments) == 5 || (len(segments) == 6 && segmentSafe(segments[5]))
	case "workflows":
		return allowedWorkflows(segments)
	case "permission-snapshots":
		return len(segments) == 5 || (len(segments) == 6 && segmentSafe(segments[5]))
	case "evidence-graph":
		return len(segments) >= 6 && segments[5] == "claims" && (len(segments) == 6 || (len(segments) == 7 && segmentSafe(segments[6])))
	case "confidence":
		return len(segments) >= 6 && segments[5] == "claims" && (len(segments) == 6 || (len(segments) == 7 && segmentSafe(segments[6])))
	case "knowledge":
		return len(segments) == 5 || (len(segments) == 6 && segmentSafe(segments[5]))
	case "integrations":
		return allowedIntegrations(segments)
	default:
		return false
	}
}

func allowedWorkPlans(segments []string) bool {
	if len(segments) == 5 {
		return true
	}
	if len(segments) == 6 {
		return segmentSafe(segments[5])
	}
	return len(segments) == 7 && segmentSafe(segments[5]) && segments[6] == "resume"
}

func allowedWorkTasks(segments []string) bool {
	if len(segments) == 5 {
		return true
	}
	if len(segments) != 6 {
		return false
	}
	switch segments[5] {
	case "open", "mine", "blocked", "next":
		return true
	default:
		return segmentSafe(segments[5])
	}
}

func allowedWorkflows(segments []string) bool {
	if len(segments) == 5 {
		return true
	}
	if len(segments) == 6 {
		return segmentSafe(segments[5])
	}
	if len(segments) == 7 {
		return segmentSafe(segments[5]) && segments[6] == "agent-definitions"
	}
	return len(segments) == 8 && segmentSafe(segments[5]) && segments[6] == "agent-definitions" && segmentSafe(segments[7])
}

func allowedIntegrations(segments []string) bool {
	if len(segments) == 5 {
		return true
	}
	if len(segments) == 6 {
		return segments[5] == "counts" || segments[5] == "search"
	}
	if len(segments) == 7 {
		return (segments[5] == "jira" && segments[6] == "issues") ||
			(segments[5] == "confluence" && segments[6] == "pages") ||
			((segments[5] == "jira" || segments[5] == "confluence") && segments[6] == "status")
	}
	return len(segments) == 8 &&
		((segments[5] == "jira" && segments[6] == "issues" && segmentSafe(segments[7])) ||
			(segments[5] == "confluence" && segments[6] == "pages" && segmentSafe(segments[7])))
}

func pathSegments(rawPath string) []string {
	if rawPath == "" || !strings.HasPrefix(rawPath, "/") || strings.Contains(rawPath, "//") {
		return nil
	}
	trimmed := strings.Trim(rawPath, "/")
	if trimmed == "" {
		return nil
	}
	segments := strings.Split(trimmed, "/")
	for _, segment := range segments {
		if !segmentSafe(segment) {
			return nil
		}
	}
	return segments
}

func segmentSafe(segment string) bool {
	if segment == "" || segment == "." || segment == ".." {
		return false
	}
	for _, char := range segment {
		if char >= 'a' && char <= 'z' {
			continue
		}
		if char >= 'A' && char <= 'Z' {
			continue
		}
		if char >= '0' && char <= '9' {
			continue
		}
		if char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func copySafeRequestHeaders(dst, src http.Header) {
	for _, key := range []string{"Accept", "Accept-Language", "Cache-Control", "Last-Event-ID", "User-Agent", "X-Request-ID"} {
		for _, value := range src.Values(key) {
			dst.Add(key, value)
		}
	}
}

func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		if shouldDropResponseHeader(key) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func shouldDropResponseHeader(key string) bool {
	key = http.CanonicalHeaderKey(key)
	if strings.HasPrefix(key, "Access-Control-") {
		return true
	}
	switch key {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}

func isSSE(header http.Header) bool {
	return strings.HasPrefix(strings.ToLower(header.Get("Content-Type")), "text/event-stream")
}

func copyStreaming(w http.ResponseWriter, body io.Reader) {
	flusher, _ := w.(http.Flusher)
	buffer := make([]byte, 32*1024)
	for {
		n, err := body.Read(buffer)
		if n > 0 {
			_, _ = w.Write(buffer[:n])
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

func noRedirect(*http.Request, []*http.Request) error {
	return errors.New("redirects disabled")
}
