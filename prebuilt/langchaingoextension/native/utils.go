package native

import "strings"

type llmOption struct {
	Provider string
	Model    string
	APIKey   string
	BaseURL  string
}

func parseLLMConnectionString(connectionString string) llmOption {
	items := strings.Split(connectionString, ";")
	return llmOption{
		Provider: items[0],
		BaseURL:  items[1],
		APIKey:   items[2],
		Model:    items[3],
	}
}
