package config_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"go.dagstack.dev/config"
)

// devVaultSource is a minimal Vault SecretSource for the conformance
// runner. It speaks Vault's HTTP API directly so the main module's
// go.mod stays free of the official vault SDK transitive deps. The
// production adapter lives in the sub-module
// `go.dagstack.dev/config/vault` and uses
// github.com/hashicorp/vault/api.
//
// Phase 2 surface only: KV v2 reads with optional ?version=N and
// #field projection. Token-only auth (matches dev-mode docker-compose
// + seed.sh).
type devVaultSource struct {
	addr  string
	token string
}

func (*devVaultSource) Scheme() string { return "vault" }

func (s *devVaultSource) ID() string { return "vault:" + s.addr }

func (s *devVaultSource) Resolve(ctx context.Context, path string) (config.SecretValue, error) {
	mountPoint, keyPath, version, field, err := parseDevVaultPath(path)
	if err != nil {
		return config.SecretValue{}, err
	}

	url := s.addr + "/v1/" + mountPoint + "/data/" + keyPath
	if version > 0 {
		url += "?version=" + strconv.Itoa(version)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return config.SecretValue{}, &config.Error{
			Reason:   config.ReasonSecretBackendUnavailable,
			Details:  fmt.Sprintf("Vault HTTP request build failed: %v", err),
			SourceID: s.ID(),
			Wrapped:  err,
		}
	}
	req.Header.Set("X-Vault-Token", s.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return config.SecretValue{}, &config.Error{
			Reason:   config.ReasonSecretBackendUnavailable,
			Details:  fmt.Sprintf("Vault read of %s/%s failed: %v", mountPoint, keyPath, err),
			SourceID: s.ID(),
			Wrapped:  err,
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusForbidden {
		return config.SecretValue{}, &config.Error{
			Reason: config.ReasonSecretPermissionDenied,
			Details: fmt.Sprintf("Vault read of %s/%s failed: rejected (403)",
				mountPoint, keyPath),
			SourceID: s.ID(),
		}
	}
	if resp.StatusCode == http.StatusNotFound {
		return config.SecretValue{}, &config.Error{
			Reason: config.ReasonSecretUnresolved,
			Details: fmt.Sprintf("Vault read of %s/%s failed: not found (404)",
				mountPoint, keyPath),
			SourceID: s.ID(),
		}
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return config.SecretValue{}, &config.Error{
			Reason: config.ReasonSecretBackendUnavailable,
			Details: fmt.Sprintf("Vault read of %s/%s failed: HTTP %d: %s",
				mountPoint, keyPath, resp.StatusCode, string(body)),
			SourceID: s.ID(),
		}
	}

	var envelope struct {
		Data struct {
			Data     map[string]any `json:"data"`
			Metadata struct {
				Version int `json:"version"`
			} `json:"metadata"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return config.SecretValue{}, &config.Error{
			Reason: config.ReasonSecretBackendUnavailable,
			Details: fmt.Sprintf("Vault response for %s/%s has unexpected envelope shape: %v",
				mountPoint, keyPath, err),
			SourceID: s.ID(),
			Wrapped:  err,
		}
	}

	data := envelope.Data.Data
	if len(data) == 0 {
		return config.SecretValue{}, &config.Error{
			Reason: config.ReasonSecretUnresolved,
			Details: fmt.Sprintf("Vault %s/%s contains an empty secret",
				mountPoint, keyPath),
			SourceID: s.ID(),
		}
	}

	var rawValue any
	if field != "" {
		v, ok := data[field]
		if !ok {
			keys := sortedKeys(data)
			return config.SecretValue{}, &config.Error{
				Reason: config.ReasonSecretUnresolved,
				Details: fmt.Sprintf("Vault %s/%s has no field %q (available keys: %v)",
					mountPoint, keyPath, field, keys),
				SourceID: s.ID(),
			}
		}
		rawValue = v
	} else if len(data) > 1 {
		keys := sortedKeys(data)
		return config.SecretValue{}, &config.Error{
			Reason: config.ReasonSecretUnresolved,
			Details: fmt.Sprintf(
				"reference resolved to object; specify a sub-key with '#field' (available keys: %v)",
				keys),
			SourceID: s.ID(),
		}
	} else {
		for _, v := range data {
			rawValue = v
		}
	}

	value, ok := rawValue.(string)
	if !ok {
		value = fmt.Sprintf("%v", rawValue)
	}
	return config.SecretValue{
		Value:    value,
		SourceID: s.ID(),
		Version:  strconv.Itoa(envelope.Data.Metadata.Version),
	}, nil
}

func (*devVaultSource) Close() error { return nil }

func parseDevVaultPath(path string) (mount, key string, version int, field string, err error) {
	if idx := strings.Index(path, "#"); idx >= 0 {
		field = path[idx+1:]
		path = path[:idx]
	}
	if idx := strings.Index(path, "?"); idx >= 0 {
		query := path[idx+1:]
		path = path[:idx]
		for _, kv := range strings.Split(query, "&") {
			eqIdx := strings.Index(kv, "=")
			if eqIdx < 0 {
				return "", "", 0, "", &config.Error{
					Reason:  config.ReasonSecretUnresolved,
					Details: fmt.Sprintf("Vault query parameter %q is missing '='", kv),
				}
			}
			k, v := kv[:eqIdx], kv[eqIdx+1:]
			if k == "version" {
				n, parseErr := strconv.Atoi(v)
				if parseErr != nil {
					return "", "", 0, "", &config.Error{
						Reason: config.ReasonSecretUnresolved,
						Details: fmt.Sprintf("Vault path has invalid ?version= value %q: %v",
							v, parseErr),
					}
				}
				version = n
			} else {
				return "", "", 0, "", &config.Error{
					Reason: config.ReasonSecretUnresolved,
					Details: fmt.Sprintf("Vault path has unknown query parameter %q; "+
						"only 'version' is recognised in Phase 2", k),
				}
			}
		}
	}
	slashIdx := strings.Index(path, "/")
	if slashIdx < 0 {
		return "", "", 0, "", &config.Error{
			Reason: config.ReasonSecretUnresolved,
			Details: fmt.Sprintf("Vault path %q does not include a mount-point segment "+
				"(expected '<mount>/<key-path>', e.g. 'secret/dagstack/db')", path),
		}
	}
	return path[:slashIdx], path[slashIdx+1:], version, field, nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Compile-time assertion that devVaultSource satisfies the contract.
var _ config.SecretSource = (*devVaultSource)(nil)

// Touch errors to silence unused imports when the package is built
// without devVaultSource exercised.
var _ = errors.New
