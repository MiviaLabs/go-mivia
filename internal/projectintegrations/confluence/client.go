package confluence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectintegrations"
)

const provider = "confluence"

type Client struct {
	baseURL    string
	httpClient *http.Client
}

type Options struct {
	BaseURL    string
	HTTPClient *http.Client
	Timeout    time.Duration
}

type SearchResponse struct {
	Results []json.RawMessage `json:"results"`
	Links   map[string]string `json:"_links,omitempty"`
}

type PageResponse struct {
	Raw json.RawMessage
}

func NewClient(options Options) Client {
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: options.Timeout}
	}
	return Client{
		baseURL:    strings.TrimRight(options.BaseURL, "/"),
		httpClient: httpClient,
	}
}

func (client Client) SearchPages(ctx context.Context, credentials projectintegrations.Credentials, cql string, limit int, cursor ...string) (SearchResponse, error) {
	values := url.Values{}
	values.Set("cql", cql)
	if limit > 0 {
		values.Set("limit", strconvItoa(limit))
	}
	if len(cursor) > 0 && strings.TrimSpace(cursor[0]) != "" {
		values.Set("cursor", strings.TrimSpace(cursor[0]))
	}
	var response SearchResponse
	if err := client.doJSON(ctx, credentials, "/wiki/rest/api/search?"+values.Encode(), "search", &response); err != nil {
		return SearchResponse{}, err
	}
	return response, nil
}

func (response SearchResponse) NextCursor() string {
	next := strings.TrimSpace(response.Links["next"])
	if next == "" {
		return ""
	}
	parsed, err := url.Parse(next)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(parsed.Query().Get("cursor"))
}

func (client Client) GetPage(ctx context.Context, credentials projectintegrations.Credentials, pageID string, bodyRepresentation string) (PageResponse, error) {
	values := url.Values{}
	if strings.TrimSpace(bodyRepresentation) != "" {
		values.Set("body-format", strings.TrimSpace(bodyRepresentation))
	}
	path := "/wiki/api/v2/pages/" + url.PathEscape(pageID)
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var raw json.RawMessage
	if err := client.doJSON(ctx, credentials, path, "get_page", &raw); err != nil {
		return PageResponse{}, err
	}
	return PageResponse{Raw: raw}, nil
}

func (client Client) doJSON(ctx context.Context, credentials projectintegrations.Credentials, path, operation string, responseBody any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+path, nil)
	if err != nil {
		return projectintegrations.RequestError(provider, operation)
	}
	req.SetBasicAuth(credentials.Email, credentials.APIToken)
	req.Header.Set("Accept", "application/json")
	resp, err := client.httpClient.Do(req)
	if err != nil {
		return projectintegrations.RequestError(provider, operation)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return projectintegrations.ProviderErrorFromStatus(provider, operation, resp.StatusCode, projectintegrations.RetryAfter(resp.Header.Get("Retry-After")))
	}
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(responseBody); err != nil {
		return projectintegrations.DecodeError(provider, operation)
	}
	return nil
}

func strconvItoa(value int) string {
	return strconv.FormatInt(int64(value), 10)
}
