package config_test

// Test helpers live in the config_test package. Phase A exposes
// NewSubscription / NewInactiveSubscription as public API (used by
// third-party Watcher implementations and the internal subscription
// manager); tests exercise them directly — no unsafe / reflect tricks.
