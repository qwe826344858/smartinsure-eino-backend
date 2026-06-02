package llm

import (
	"os"

	"smartinsure-eino-backend/internal/config"
)

type EndpointConfig struct {
	Name  string
	Model string
	Key   string
	Base  string
}

type ProviderConfig struct {
	Name        string
	Description string
	Model       string
	Key         string
	Base        string
	Endpoints   []EndpointConfig
}

type ProviderStatus struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Model       string   `json:"model"`
	Available   bool     `json:"available"`
	IsDefault   bool     `json:"is_default"`
	Stages      []string `json:"stages"`
}

type Registry struct {
	providers       map[string]ProviderConfig
	routing         map[string]string
	defaultProvider string
}

func NewRegistry(file config.LLMProviderFile, settings config.Settings) *Registry {
	reg := &Registry{
		providers:       map[string]ProviderConfig{},
		routing:         map[string]string{},
		defaultProvider: file.DefaultProvider,
	}
	for stage, route := range file.Routing {
		if route.Provider != "" {
			reg.routing[stage] = route.Provider
		}
	}
	for name, raw := range file.Providers {
		if !raw.Enabled {
			continue
		}
		cfg := resolveProvider(name, raw, settings)
		reg.providers[name] = cfg
	}
	if settings.LLMProvider != "" {
		reg.defaultProvider = settings.LLMProvider
	}
	return reg
}

func LoadRegistry(path string, settings config.Settings) (*Registry, error) {
	file, err := config.LoadLLMProviderFile(path)
	if err != nil {
		return nil, err
	}
	return NewRegistry(file, settings), nil
}

func (r *Registry) Providers() map[string]ProviderConfig {
	out := make(map[string]ProviderConfig, len(r.providers))
	for name, cfg := range r.providers {
		out[name] = cfg
	}
	return out
}

func (r *Registry) AvailableProviders() []string {
	var names []string
	for name, cfg := range r.providers {
		if cfg.Key != "" {
			names = append(names, name)
		}
	}
	return names
}

func (r *Registry) GetProvider(name string) (ProviderConfig, bool) {
	cfg, ok := r.providers[name]
	return cfg, ok
}

func (r *Registry) Default() (ProviderConfig, bool) {
	cfg, ok := r.providers[r.defaultProvider]
	return cfg, ok
}

func (r *Registry) ForStage(stage string) ProviderConfig {
	name := r.routing[stage]
	if name == "" {
		name = r.defaultProvider
	}
	if cfg, ok := r.providers[name]; ok && cfg.Key != "" {
		return cfg
	}
	if cfg, ok := r.providers[r.defaultProvider]; ok && cfg.Key != "" {
		return cfg
	}
	for _, cfg := range r.providers {
		if cfg.Key != "" {
			return cfg
		}
	}
	if cfg, ok := r.providers[r.defaultProvider]; ok {
		return cfg
	}
	return ProviderConfig{Name: "none"}
}

func (r *Registry) ListProviders() []ProviderStatus {
	statuses := make([]ProviderStatus, 0, len(r.providers))
	for name, cfg := range r.providers {
		var stages []string
		for stage, providerName := range r.routing {
			if providerName == name {
				stages = append(stages, stage)
			}
		}
		statuses = append(statuses, ProviderStatus{
			Name:        name,
			Description: cfg.Description,
			Model:       cfg.Model,
			Available:   cfg.Key != "",
			IsDefault:   name == r.defaultProvider,
			Stages:      stages,
		})
	}
	return statuses
}

func resolveProvider(name string, raw config.ProviderFileConfig, settings config.Settings) ProviderConfig {
	key := env(raw.APIKeyEnv)
	if settings.LLMAPIKey != "" {
		key = settings.LLMAPIKey
	}
	base := env(raw.APIBaseEnv)
	if base == "" {
		base = raw.DefaultBase
	}
	if settings.LLMAPIBase != "" {
		base = settings.LLMAPIBase
	}

	endpoints := make([]EndpointConfig, 0, len(raw.Endpoints))
	for _, ep := range raw.Endpoints {
		model := modelName(firstNonEmpty(ep.Prefix, raw.Prefix), firstNonEmpty(ep.DefaultModel, raw.DefaultModel))
		endpoints = append(endpoints, EndpointConfig{
			Name:  firstNonEmpty(ep.Name, ep.APIBase),
			Model: model,
			Key:   key,
			Base:  firstNonEmpty(ep.APIBase, base),
		})
	}
	if len(endpoints) == 0 {
		endpoints = append(endpoints, EndpointConfig{
			Name:  "default",
			Model: modelName(raw.Prefix, firstNonEmpty(settings.LLMModel, raw.DefaultModel)),
			Key:   key,
			Base:  base,
		})
	}
	return ProviderConfig{
		Name:        name,
		Description: raw.Description,
		Model:       endpoints[0].Model,
		Key:         endpoints[0].Key,
		Base:        endpoints[0].Base,
		Endpoints:   endpoints,
	}
}

func modelName(prefix, model string) string {
	if prefix == "" || model == "" {
		return model
	}
	return prefix + "/" + model
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func env(key string) string {
	if key == "" {
		return ""
	}
	return os.Getenv(key)
}
