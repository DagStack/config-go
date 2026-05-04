// Package config is the Go binding for dagstack/config-spec —
// YAML configuration with env interpolation, deep-merge layering,
// typed sections via struct tags, runtime reconfigure.
//
// Spec: https://github.com/dagstack/config-spec (ADR-0001 v2.2).
// Reference implementation: https://github.com/dagstack/config-python.
//
// Status: Phase 2 secrets shipped (v0.4.x). Phase 1 (file sources,
// env interpolation, deep-merge layering, struct-tag typed sections,
// canonical JSON) is stable. Phase 2 adds ${secret:<scheme>:<path>}
// references and the SecretSource adapter contract (ADR-0002); the
// pilot Vault adapter ships as a separate sub-module
// go.dagstack.dev/config/vault.
//
// Typical usage:
//
//	cfg, err := config.Load(ctx, "app-config.yaml")
//	if err != nil {
//	    return err
//	}
//	host, err := cfg.GetString("database.host")
//	ttl, _ := cfg.GetIntDefault("cache.ttl_min", 10)
//
//	type DatabaseConfig struct {
//	    Host     string `yaml:"host"`
//	    Password string `yaml:"password"`
//	}
//	var db DatabaseConfig
//	if err := cfg.GetSection("database", &db); err != nil {
//	    return err
//	}
//
// Phase 2 ships RefreshSecrets(ctx) — the manual rotation hook for
// secret references; push-based rotation lands in Phase 3.
// OnChange / OnSectionChange / Reload remain inactive through Phase 2
// for ConfigSource-derived data (file sources do not emit push
// events); activation lands in Phase 3+ alongside push-capable file
// sources.
package config
