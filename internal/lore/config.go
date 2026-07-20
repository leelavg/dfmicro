package lore

import (
	_ "embed"
	"encoding/json"
	"sync"
	"time"
)

//go:embed defaults.json
var embeddedDefaults []byte

type config struct {
	DocsBaseURL        string `json:"docsBaseURL"`
	SolrBaseURL        string `json:"solrBaseURL"`
	MaxRetries         int    `json:"maxRetries"`
	RetryDelaySeconds  int    `json:"retryDelaySeconds"`
	HTTPTimeoutSeconds int    `json:"httpTimeoutSeconds"`
	DefaultProduct     string `json:"defaultProduct"`
}

var loadConfig = sync.OnceValue(func() config {
	var cfg config
	if err := json.Unmarshal(embeddedDefaults, &cfg); err != nil {
		panic(err)
	}
	return cfg
})

func (c config) RetryDelay() time.Duration {
	return time.Duration(c.RetryDelaySeconds) * time.Second
}

func (c config) HTTPTimeout() time.Duration {
	return time.Duration(c.HTTPTimeoutSeconds) * time.Second
}
