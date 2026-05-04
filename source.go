package config

import "context"

// Tree is the language-neutral representation of a parsed config
// source — nested maps / slices / scalars (string, int, float, bool, nil).
//
// A Tree follows the YAML 1.2 decoder convention of yaml.v3. Type
// coercion (GetInt, GetBool) happens at the Config.Get* layer, not
// here.
//
// Tree is a defined type (not an alias) so Phase B can add methods
// — Walk, Validate, String — without a breaking change to the public
// signature.
type Tree map[string]any

// Source abstracts over where configuration is loaded from — a file,
// etcd, Consul, Vault, an HTTP endpoint, or a custom backend.
//
// Phase 1 bundles three source implementations: YamlFileSource,
// JsonFileSource, DictSource. Phase 2+ ships etcd / Consul / Vault /
// HTTP / SQL / K8s adapters.
//
// Users write their own Source by implementing this interface — see
// guides/custom-source at https://config.dagstack.dev.
//
// Go idiom: Load takes a context.Context for cancellation and timeout
// support (spec §4 leaves synchronous vs. asynchronous loading to each
// binding). Watch and Close are optional via the Watcher and Closer
// extension interfaces.
type Source interface {
	// ID is a human-readable identifier for diagnostics, usually in
	// URI form: "yaml:app-config.yaml", "etcd://cluster/prefix".
	ID() string

	// Load returns the parsed config tree. The loader then applies env
	// interpolation (if Interpolate returns true) and merges this tree
	// with the other sources in priority order.
	Load(ctx context.Context) (Tree, error)

	// Interpolate signals whether the loader should apply ${VAR} /
	// ${VAR:-default} substitution to string leaves of the returned
	// tree. File sources return true; pre-rendered remote stores
	// typically return false.
	Interpolate() bool
}

// Watcher is an optional extension of Source — implement it if your
// backend supports push-based change notifications (file inotify,
// etcd watch, K8s informers, etc.).
//
// If a Source does not implement Watcher, subscriptions registered
// via Config.OnChange / Config.OnSectionChange are still accepted
// but return Subscription.Active=false with InactiveReason set to
// "no watch-capable source registered". This is not an error — it
// is a diagnostic signal.
type Watcher interface {
	// Watch registers a callback that the source invokes when its
	// underlying data changes. Callback runs asynchronously
	// (fire-and-forget); the source does not wait for completion.
	//
	// Watch returns an unsubscribe function. After calling it, the
	// callback is guaranteed not to fire again (idempotent — repeat
	// calls are no-op). The Config-level subscription manager wraps
	// this into a public *Subscription handle with the right Path /
	// Active / InactiveReason.
	Watch(ctx context.Context, callback func(Tree)) (unsubscribe func(), err error)
}

// Closer is an optional extension of Source — implement it to release
// resources (file handles, network connections, background goroutines)
// when Config.Close is called.
//
// Close must be idempotent: Config may invoke it multiple times during
// error paths and shutdown.
type Closer interface {
	Close() error
}
