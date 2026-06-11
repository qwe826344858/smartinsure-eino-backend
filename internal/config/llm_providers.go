package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type LLMProviderFile struct {
	DefaultProvider string
	Routing         map[string]RouteConfig
	Providers       map[string]ProviderFileConfig
}

type RouteConfig struct {
	Provider string
	Model    string
}

type ProviderFileConfig struct {
	Description  string
	Prefix       string
	DefaultModel string
	APIKeyEnv    string
	APIBaseEnv   string
	DefaultBase  string
	Enabled      bool
	Endpoints    []EndpointFileConfig
}

type EndpointFileConfig struct {
	Name         string
	Prefix       string
	DefaultModel string
	APIBase      string
}

// LoadLLMProviderFile parses the repository's simple provider YAML without
// pulling an external YAML dependency into this package.
func LoadLLMProviderFile(path string) (LLMProviderFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return LLMProviderFile{}, err
	}
	defer file.Close()

	cfg := LLMProviderFile{
		Routing:   map[string]RouteConfig{},
		Providers: map[string]ProviderFileConfig{},
	}
	section := ""
	currentRoute := ""
	currentProvider := ""
	currentEndpoint := -1

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		raw := stripComment(scanner.Text())
		if strings.TrimSpace(raw) == "" {
			continue
		}
		indent := leadingSpaces(raw)
		line := strings.TrimSpace(raw)

		if indent == 0 {
			currentRoute = ""
			currentProvider = ""
			currentEndpoint = -1
			if strings.HasSuffix(line, ":") {
				section = strings.TrimSuffix(line, ":")
				continue
			}
			key, value, ok := splitKV(line)
			if ok && key == "default_provider" {
				cfg.DefaultProvider = unquote(value)
			}
			continue
		}

		switch section {
		case "routing":
			if indent == 2 && strings.HasSuffix(line, ":") {
				currentRoute = strings.TrimSuffix(line, ":")
				cfg.Routing[currentRoute] = RouteConfig{}
				continue
			}
			if indent >= 4 && currentRoute != "" {
				key, value, ok := splitKV(line)
				if !ok {
					continue
				}
				route := cfg.Routing[currentRoute]
				switch key {
				case "provider":
					route.Provider = unquote(value)
				case "model":
					route.Model = unquote(value)
				}
				cfg.Routing[currentRoute] = route
			}
		case "providers":
			if indent == 2 && strings.HasSuffix(line, ":") {
				currentProvider = strings.TrimSuffix(line, ":")
				cfg.Providers[currentProvider] = ProviderFileConfig{Enabled: true}
				currentEndpoint = -1
				continue
			}
			if currentProvider == "" {
				continue
			}
			provider := cfg.Providers[currentProvider]
			if strings.HasPrefix(line, "- ") {
				ep := EndpointFileConfig{}
				provider.Endpoints = append(provider.Endpoints, ep)
				currentEndpoint = len(provider.Endpoints) - 1
				cfg.Providers[currentProvider] = provider
				line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
				if line == "" {
					continue
				}
			}
			key, value, ok := splitKV(line)
			if !ok {
				continue
			}
			if currentEndpoint >= 0 && indent >= 6 {
				ep := provider.Endpoints[currentEndpoint]
				switch key {
				case "name":
					ep.Name = unquote(value)
				case "prefix":
					ep.Prefix = unquote(value)
				case "default_model":
					ep.DefaultModel = unquote(value)
				case "api_base":
					ep.APIBase = unquote(value)
				}
				provider.Endpoints[currentEndpoint] = ep
			} else {
				switch key {
				case "description":
					provider.Description = unquote(value)
				case "prefix":
					provider.Prefix = unquote(value)
				case "default_model":
					provider.DefaultModel = unquote(value)
				case "api_key_env":
					provider.APIKeyEnv = unquote(value)
				case "api_base_env":
					provider.APIBaseEnv = unquote(value)
				case "default_base":
					provider.DefaultBase = unquote(value)
				case "enabled":
					provider.Enabled = unquote(value) != "false"
				}
			}
			cfg.Providers[currentProvider] = provider
		}
	}
	if err := scanner.Err(); err != nil {
		return LLMProviderFile{}, err
	}
	if cfg.DefaultProvider == "" {
		return LLMProviderFile{}, fmt.Errorf("llm provider config missing default_provider")
	}
	return cfg, nil
}

func splitKV(line string) (string, string, bool) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

func stripComment(line string) string {
	inQuote := false
	var quote byte
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\'', '"':
			if !inQuote {
				inQuote = true
				quote = line[i]
			} else if quote == line[i] {
				inQuote = false
			}
		case '#':
			if !inQuote {
				return line[:i]
			}
		}
	}
	return line
}

func leadingSpaces(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func unquote(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	return value
}
