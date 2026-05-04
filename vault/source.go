// Package vault provides a HashiCorp Vault SecretSource adapter
// (ADR-0002 §6) for go.dagstack.dev/config.
//
// Sub-module: lives in its own go.mod so the official
// hashicorp/vault/api dependency does not leak into binaries that
// only use file sources. Import as
// `go.dagstack.dev/config/vault` separately from
// `go.dagstack.dev/config`.
//
// Phase 2 scope (ADR-0002 §6.1 / §6.2):
//
//   - KV v2 only.
//   - Token + AppRole auth (mandatory) + Kubernetes ServiceAccount
//     auth (optional).
//   - Namespace support (Vault Enterprise).
//   - ?version=N query.
//   - #field projection.
//
// Token self-renewal lands alongside the Phase 3 rotation hook in
// the upstream `config` package.
//
// Stutter-avoidance note: this sub-package extracts secrets out of
// the main `config` package, so the canonical type name is
// `vault.Source`, not `vault.VaultSource`. Bindings importing it
// will write `vault.NewSource(...)` (no stutter at the call site).
package vault

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	vaultapi "github.com/hashicorp/vault/api"
	"go.dagstack.dev/config"
)

// ── Auth descriptors ──────────────────────────────────────────────────

// Auth is the discriminated union of supported Vault auth methods.
// New methods (AWS IAM, JWT/OIDC, TLS client certificate) land per
// operator demand in Phase 3.
type Auth interface{ isAuth() }

// TokenAuth — direct Vault token. Simplest case; covers any deployment
// that already injects a token via init-container or operator action.
type TokenAuth struct{ Token string }

func (TokenAuth) isAuth() {}

// AppRoleAuth — AppRole authentication, the production CI/CD pipeline
// default.
type AppRoleAuth struct {
	RoleID     string
	SecretID   string
	MountPoint string // default: "approle"
}

func (AppRoleAuth) isAuth() {}

// KubernetesAuth — Kubernetes ServiceAccount authentication. Reads
// the SA JWT from the standard projected-token path; one
// auth/kubernetes/login round-trip per Source lifetime (no in-flight
// renewal in Phase 2).
type KubernetesAuth struct {
	Role       string
	JWTPath    string // default: /var/run/secrets/kubernetes.io/serviceaccount/token
	MountPoint string // default: "kubernetes"
}

func (KubernetesAuth) isAuth() {}

// ── Source ────────────────────────────────────────────────────────────

// Source implements config.SecretSource for HashiCorp Vault KV v2.
//
// Path layout: the user-visible path is what `vault kv get` accepts
// (e.g. `secret/dagstack/prod/openai`). The first segment is the KV
// v2 mount point (default Vault setup uses `secret`); the remainder
// is the logical key path. The Vault HTTP API expects
// `<mount>/data/<path>` — this Source rewrites it internally.
//
// Path also supports the optional ?version=N query (read a specific
// KV v2 version) and the #field projection (pluck a sub-key from a
// multi-key secret) per ADR-0002 §6.3.
type Source struct {
	addr      string
	namespace string
	id        string
	client    *vaultapi.Client
}

// Option configures NewSource.
type Option func(*sourceOptions)

type sourceOptions struct {
	addr      string
	namespace string
	auth      Auth
	tlsConfig *vaultapi.TLSConfig
}

// WithNamespace sets a Vault Enterprise namespace.
func WithNamespace(ns string) Option {
	return func(o *sourceOptions) { o.namespace = ns }
}

// WithTLSConfig overrides the default TLS configuration.
func WithTLSConfig(cfg *vaultapi.TLSConfig) Option {
	return func(o *sourceOptions) { o.tlsConfig = cfg }
}

// NewSource constructs a Vault Source. addr is the base URL of the
// Vault server (e.g. "https://vault.example.com"). auth selects the
// authentication method. opts apply additional options
// (WithNamespace, WithTLSConfig).
func NewSource(addr string, auth Auth, opts ...Option) (*Source, error) {
	options := &sourceOptions{addr: addr, auth: auth}
	for _, opt := range opts {
		opt(options)
	}

	cfg := vaultapi.DefaultConfig()
	cfg.Address = addr
	if options.tlsConfig != nil {
		if err := cfg.ConfigureTLS(options.tlsConfig); err != nil {
			return nil, &config.Error{
				Reason:  config.ReasonSecretBackendUnavailable,
				Details: fmt.Sprintf("Vault TLS configuration error: %v", err),
				Wrapped: err,
			}
		}
	}

	client, err := vaultapi.NewClient(cfg)
	if err != nil {
		return nil, &config.Error{
			Reason:  config.ReasonSecretBackendUnavailable,
			Details: fmt.Sprintf("Vault client init failed: %v", err),
			Wrapped: err,
		}
	}
	if options.namespace != "" {
		client.SetNamespace(options.namespace)
	}

	id := "vault:" + addr
	if options.namespace != "" {
		id += "?namespace=" + options.namespace
	}

	src := &Source{
		addr:      addr,
		namespace: options.namespace,
		id:        id,
		client:    client,
	}
	if err := src.authenticate(auth); err != nil {
		return nil, err
	}
	return src, nil
}

// Scheme implements config.SecretSource. Hard-coded to "vault".
func (*Source) Scheme() string { return "vault" }

// ID implements config.SecretSource.
func (s *Source) ID() string { return s.id }

func (s *Source) authenticate(auth Auth) error {
	switch a := auth.(type) {
	case TokenAuth:
		s.client.SetToken(a.Token)
	case AppRoleAuth:
		mount := a.MountPoint
		if mount == "" {
			mount = "approle"
		}
		secret, err := s.client.Logical().Write(
			"auth/"+mount+"/login",
			map[string]any{
				"role_id":   a.RoleID,
				"secret_id": a.SecretID,
			},
		)
		if err != nil {
			return s.translateAuthError("AppRole", err)
		}
		if secret == nil || secret.Auth == nil {
			return &config.Error{
				Reason:   config.ReasonSecretBackendUnavailable,
				Details:  "Vault AppRole login returned no client_token",
				SourceID: s.id,
			}
		}
		s.client.SetToken(secret.Auth.ClientToken)
	case KubernetesAuth:
		jwtPath := a.JWTPath
		if jwtPath == "" {
			jwtPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
		}
		jwt, err := os.ReadFile(jwtPath)
		if err != nil {
			return &config.Error{
				Reason: config.ReasonSecretBackendUnavailable,
				Details: fmt.Sprintf(
					"cannot read Kubernetes ServiceAccount token at %q: %v "+
						"(running outside a pod? misconfigured projected token?)",
					jwtPath, err),
				SourceID: s.id,
				Wrapped:  err,
			}
		}
		mount := a.MountPoint
		if mount == "" {
			mount = "kubernetes"
		}
		secret, err := s.client.Logical().Write(
			"auth/"+mount+"/login",
			map[string]any{
				"role": a.Role,
				"jwt":  strings.TrimSpace(string(jwt)),
			},
		)
		if err != nil {
			return s.translateAuthError("Kubernetes", err)
		}
		if secret == nil || secret.Auth == nil {
			return &config.Error{
				Reason:   config.ReasonSecretBackendUnavailable,
				Details:  "Vault Kubernetes login returned no client_token",
				SourceID: s.id,
			}
		}
		s.client.SetToken(secret.Auth.ClientToken)
	default:
		return &config.Error{
			Reason:   config.ReasonValidationFailed,
			Details:  fmt.Sprintf("unknown Vault auth type: %T", auth),
			SourceID: s.id,
		}
	}
	return nil
}

func (s *Source) translateAuthError(method string, err error) error {
	if isVaultForbidden(err) {
		return &config.Error{
			Reason:   config.ReasonSecretPermissionDenied,
			Details:  fmt.Sprintf("Vault %s login rejected: %v", method, err),
			SourceID: s.id,
			Wrapped:  err,
		}
	}
	return &config.Error{
		Reason:   config.ReasonSecretBackendUnavailable,
		Details:  fmt.Sprintf("Vault %s login failed: %v", method, err),
		SourceID: s.id,
		Wrapped:  err,
	}
}

// Resolve implements config.SecretSource.
func (s *Source) Resolve(ctx context.Context, path string) (config.SecretValue, error) {
	if err := ctx.Err(); err != nil {
		return config.SecretValue{}, &config.Error{
			Reason:   config.ReasonSecretBackendUnavailable,
			Details:  "context cancelled before Vault read: " + err.Error(),
			SourceID: s.id,
			Wrapped:  err,
		}
	}

	parsed, err := parseVaultPath(path)
	if err != nil {
		return config.SecretValue{}, err
	}

	// KV v2 path: <mount>/data/<key>?version=N
	apiPath := parsed.MountPoint + "/data/" + parsed.KeyPath
	var secret *vaultapi.Secret
	if parsed.Version > 0 {
		// Use the data-helper that handles ?version= properly.
		params := url.Values{}
		params.Set("version", strconv.Itoa(parsed.Version))
		secret, err = s.client.Logical().ReadWithDataWithContext(ctx, apiPath, params)
	} else {
		secret, err = s.client.Logical().ReadWithContext(ctx, apiPath)
	}
	if err != nil {
		return config.SecretValue{}, s.translateReadError(parsed, err)
	}
	if secret == nil {
		return config.SecretValue{}, &config.Error{
			Reason: config.ReasonSecretUnresolved,
			Details: fmt.Sprintf("Vault read of %s/%s failed: not found",
				parsed.MountPoint, parsed.KeyPath),
			SourceID: s.id,
		}
	}

	// KV v2 envelope: secret.Data = {"data": {...}, "metadata": {...}}.
	dataField, ok := secret.Data["data"].(map[string]any)
	if !ok || dataField == nil {
		return config.SecretValue{}, &config.Error{
			Reason: config.ReasonSecretBackendUnavailable,
			Details: fmt.Sprintf(
				"Vault response for %s/%s has unexpected envelope shape (missing 'data')",
				parsed.MountPoint, parsed.KeyPath),
			SourceID: s.id,
		}
	}

	if len(dataField) == 0 {
		return config.SecretValue{}, &config.Error{
			Reason: config.ReasonSecretUnresolved,
			Details: fmt.Sprintf("Vault %s/%s contains an empty secret",
				parsed.MountPoint, parsed.KeyPath),
			SourceID: s.id,
		}
	}

	var rawValue any
	if parsed.Field != "" {
		v, present := dataField[parsed.Field]
		if !present {
			keys := sortedKeys(dataField)
			return config.SecretValue{}, &config.Error{
				Reason: config.ReasonSecretUnresolved,
				Details: fmt.Sprintf("Vault %s/%s has no field %q (available keys: %v)",
					parsed.MountPoint, parsed.KeyPath, parsed.Field, keys),
				SourceID: s.id,
			}
		}
		rawValue = v
	} else if len(dataField) > 1 {
		// ADR-0002 §1.2 normative message — verbatim.
		keys := sortedKeys(dataField)
		return config.SecretValue{}, &config.Error{
			Reason: config.ReasonSecretUnresolved,
			Details: fmt.Sprintf(
				"reference resolved to object; specify a sub-key with '#field' (available keys: %v)",
				keys),
			SourceID: s.id,
		}
	} else {
		// Single-key envelope — unwrap.
		for _, v := range dataField {
			rawValue = v
		}
	}

	value, ok := rawValue.(string)
	if !ok {
		// KV v2 stores everything as JSON; non-string scalars need a
		// stringification step. Phase 2 SecretValue is always string.
		value = fmt.Sprintf("%v", rawValue)
	}

	out := config.SecretValue{Value: value, SourceID: s.id}
	if metadata, ok := secret.Data["metadata"].(map[string]any); ok {
		if v, ok := metadata["version"]; ok {
			out.Version = fmt.Sprintf("%v", v)
		}
	}
	return out, nil
}

func (s *Source) translateReadError(parsed parsedVaultPath, err error) error {
	if isVaultForbidden(err) {
		return &config.Error{
			Reason: config.ReasonSecretPermissionDenied,
			Details: fmt.Sprintf("Vault read of %s/%s failed: rejected (Forbidden: %v); "+
				"check the Vault policy attached to this token / role",
				parsed.MountPoint, parsed.KeyPath, err),
			SourceID: s.id,
			Wrapped:  err,
		}
	}
	if isVaultNotFound(err) {
		return &config.Error{
			Reason: config.ReasonSecretUnresolved,
			Details: fmt.Sprintf("Vault read of %s/%s failed: not found (%v)",
				parsed.MountPoint, parsed.KeyPath, err),
			SourceID: s.id,
			Wrapped:  err,
		}
	}
	return &config.Error{
		Reason: config.ReasonSecretBackendUnavailable,
		Details: fmt.Sprintf("Vault read of %s/%s failed: %v",
			parsed.MountPoint, parsed.KeyPath, err),
		SourceID: s.id,
		Wrapped:  err,
	}
}

// Close releases any resources held by the underlying Vault client.
// hashicorp/vault/api uses an http.Client; we close the underlying
// transport's idle-connection pool for cleanliness. Token revocation
// is NOT performed automatically — operators that want it call the
// raw client API themselves before Close.
func (s *Source) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	if transport, ok := s.client.CloneConfig().HttpClient.Transport.(interface {
		CloseIdleConnections()
	}); ok {
		transport.CloseIdleConnections()
	}
	return nil
}

// ── Helpers ────────────────────────────────────────────────────────────

type parsedVaultPath struct {
	MountPoint string
	KeyPath    string
	Version    int    // 0 = latest
	Field      string // "" = unset
}

func parseVaultPath(path string) (parsedVaultPath, error) {
	out := parsedVaultPath{}

	// Strip the optional `#field` tail.
	if idx := strings.Index(path, "#"); idx >= 0 {
		out.Field = path[idx+1:]
		path = path[:idx]
	}

	// Split off the optional `?query`.
	var query string
	if idx := strings.Index(path, "?"); idx >= 0 {
		query = path[idx+1:]
		path = path[:idx]
	}

	slashIdx := strings.Index(path, "/")
	if slashIdx < 0 {
		return out, &config.Error{
			Reason: config.ReasonSecretUnresolved,
			Details: fmt.Sprintf("Vault path %q does not include a mount-point segment "+
				"(expected '<mount>/<key-path>', e.g. 'secret/dagstack/db')", path),
		}
	}
	out.MountPoint = path[:slashIdx]
	out.KeyPath = path[slashIdx+1:]

	if query != "" {
		for _, kv := range strings.Split(query, "&") {
			eqIdx := strings.Index(kv, "=")
			if eqIdx < 0 {
				return out, &config.Error{
					Reason:  config.ReasonSecretUnresolved,
					Details: fmt.Sprintf("Vault query parameter %q is missing '='", kv),
				}
			}
			key := kv[:eqIdx]
			value := kv[eqIdx+1:]
			switch key {
			case "version":
				v, err := strconv.Atoi(value)
				if err != nil {
					return out, &config.Error{
						Reason: config.ReasonSecretUnresolved,
						Details: fmt.Sprintf(
							"Vault path has invalid ?version= value %q: must be an integer",
							value),
					}
				}
				out.Version = v
			default:
				return out, &config.Error{
					Reason: config.ReasonSecretUnresolved,
					Details: fmt.Sprintf(
						"Vault path has unknown query parameter %q; only 'version' "+
							"is recognised in Phase 2", key),
				}
			}
		}
	}

	return out, nil
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// isVaultForbidden detects a Vault 403 Forbidden response.
// hashicorp/vault/api returns *vaultapi.ResponseError for non-2xx.
func isVaultForbidden(err error) bool {
	if err == nil {
		return false
	}
	var rerr *vaultapi.ResponseError
	if errorsAs(err, &rerr) && rerr.StatusCode == 403 {
		return true
	}
	return false
}

// isVaultNotFound detects a Vault 404 Not Found response.
func isVaultNotFound(err error) bool {
	if err == nil {
		return false
	}
	var rerr *vaultapi.ResponseError
	if errorsAs(err, &rerr) && rerr.StatusCode == 404 {
		return true
	}
	return false
}

// errorsAs is a thin wrapper around stdlib errors.As to keep imports minimal.
func errorsAs(err error, target any) bool {
	// We import standard errors via a single helper to avoid sprinkling
	// `errors.As` everywhere; the wrapper makes the call sites readable.
	return goErrorsAs(err, target)
}

// Compile-time check that *Source satisfies config.SecretSource.
var _ config.SecretSource = (*Source)(nil)

// Sentinel for time-related comparisons inside Resolve when needed.
var _ = time.Time{}
