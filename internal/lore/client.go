package lore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"dfmicro/internal/support"
)

type solrClient struct {
	baseURL string
	client  *http.Client
	cfg     config
}

type solrResponse struct {
	Response struct {
		NumFound int64           `json:"numFound"`
		Start    int64           `json:"start"`
		Docs     []map[string]interface{} `json:"docs"`
	} `json:"response"`
	Highlighting map[string]interface{} `json:"highlighting,omitempty"`
	Facets       map[string]interface{} `json:"facets,omitempty"`
}

type solrDocAdvisory struct {
	ID                  string `json:"id"`
	Language            string `json:"language"`
	LastModifiedDate    string `json:"lastModifiedDate"`
	PortalDescription   string `json:"portal_description"`
	PortalSolution      string `json:"portal_solution"`
	ViewURI             string `json:"view_uri"`
}

type solrDocArticle struct {
	ID                  string `json:"id"`
	LastModifiedDate    string `json:"lastModifiedDate"`
	PublishedAbstract   string `json:"publishedAbstract"`
	PublishedTitle      string `json:"publishedTitle"`
	SetLanguage         string `json:"setLanguage"`
	ViewURI             string `json:"view_uri"`
}

type solrDocSolution struct {
	ID                  string `json:"id"`
	LastModifiedDate    string `json:"lastModifiedDate"`
	PublishedAbstract   string `json:"publishedAbstract"`
	PublishedTitle      string `json:"publishedTitle"`
	SetLanguage         string `json:"setLanguage"`
	ViewURI             string `json:"view_uri"`
}

func newSolrClient(cfg config) *solrClient {
	return &solrClient{
		baseURL: cfg.SolrBaseURL,
		client:  support.NewHTTPClient(cfg.HTTPTimeout()),
		cfg:     cfg,
	}
}

func (c *solrClient) Query(ctx context.Context, params map[string]any) (*solrResponse, error) {
	q := url.Values{}
	for k, v := range params {
		switch val := v.(type) {
		case string:
			q.Set(k, val)
		case []string:
			for _, item := range val {
				q.Add(k, item)
			}
		}
	}

	reqURL := fmt.Sprintf("%s?%s", c.baseURL, q.Encode())

	var lastErr error
	for attempt := 1; attempt <= c.cfg.MaxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if err != nil {
			return nil, err
		}

		req.Header.Set("User-Agent", "dfmicro/fetch")

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("attempt %d/%d: %w", attempt, c.cfg.MaxRetries, err)
			if attempt < c.cfg.MaxRetries {
				selectTimeout(ctx, c.cfg.RetryDelay())
			}
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
			if attempt < c.cfg.MaxRetries {
				selectTimeout(ctx, c.cfg.RetryDelay())
			}
			continue
		}

		var solrResp solrResponse
		if err := json.NewDecoder(resp.Body).Decode(&solrResp); err != nil {
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}

		return &solrResp, nil
	}

	return nil, fmt.Errorf("all retries exhausted: %v", lastErr)
}

func selectTimeout(ctx context.Context, d time.Duration) {
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}
