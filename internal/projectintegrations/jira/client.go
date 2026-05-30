package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectintegrations"
)

const provider = "jira"

type Client struct {
	baseURL    string
	httpClient *http.Client
}

type Options struct {
	BaseURL    string
	HTTPClient *http.Client
	Timeout    time.Duration
}

type SearchRequest struct {
	JQL           string   `json:"jql,omitempty"`
	Fields        []string `json:"fields,omitempty"`
	MaxResults    int      `json:"maxResults,omitempty"`
	NextPageToken string   `json:"nextPageToken,omitempty"`
}

type SearchResponse struct {
	Issues        []json.RawMessage `json:"issues"`
	NextPageToken string            `json:"nextPageToken,omitempty"`
}

type IssueResponse struct {
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

func (client Client) SearchIssues(ctx context.Context, credentials projectintegrations.Credentials, request SearchRequest) (SearchResponse, error) {
	var response SearchResponse
	if err := client.doJSON(ctx, credentials, http.MethodPost, "/rest/api/3/search/jql", "search", request, &response); err != nil {
		return SearchResponse{}, err
	}
	return response, nil
}

func (client Client) GetIssue(ctx context.Context, credentials projectintegrations.Credentials, issueIDOrKey string, fields []string) (IssueResponse, error) {
	path := "/rest/api/3/issue/" + url.PathEscape(issueIDOrKey)
	if len(fields) > 0 {
		values := url.Values{}
		values.Set("fields", strings.Join(fields, ","))
		path += "?" + values.Encode()
	}
	var raw json.RawMessage
	if err := client.doJSON(ctx, credentials, http.MethodGet, path, "get_issue", nil, &raw); err != nil {
		return IssueResponse{}, err
	}
	return IssueResponse{Raw: raw}, nil
}

func (client Client) doJSON(ctx context.Context, credentials projectintegrations.Credentials, method, path, operation string, requestBody any, responseBody any) error {
	var body *bytes.Reader
	if requestBody == nil {
		body = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return projectintegrations.DecodeError(provider, operation)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, body)
	if err != nil {
		return projectintegrations.RequestError(provider, operation)
	}
	req.SetBasicAuth(credentials.Email, credentials.APIToken)
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.httpClient.Do(req)
	if err != nil {
		return projectintegrations.RequestError(provider, operation)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return projectintegrations.ProviderErrorFromStatus(provider, operation, resp.StatusCode, projectintegrations.RetryAfter(resp.Header.Get("Retry-After")))
	}
	if responseBody == nil {
		return nil
	}
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(responseBody); err != nil {
		return projectintegrations.DecodeError(provider, operation)
	}
	return nil
}
