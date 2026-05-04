package config

// NewSubscription constructs an active Subscription with the given
// path and cancellation function. It is intended for third-party
// Watcher implementations and the internal subscription manager —
// the unsubscribe callback will be invoked exactly once, on the
// first call to Unsubscribe.
func NewSubscription(path string, unsubscribe func()) *Subscription {
	return &Subscription{Active: true, Path: path, unsubscribe: unsubscribe}
}

// NewInactiveSubscription constructs a Subscription handle whose
// Active field is false. Use this to signal that a subscription was
// accepted but will never fire — typically because no registered
// Source implements Watcher, or no Source covers the requested path.
// reason is the human-readable diagnostic placed in InactiveReason.
func NewInactiveSubscription(path, reason string) *Subscription {
	return &Subscription{Active: false, Path: path, InactiveReason: reason}
}

// Subscription is a handle to a config change subscription registered
// via Config.OnChange or Config.OnSectionChange.
//
// Spec ADR-0001 §7.2 fixes the fields: Active signals whether any
// registered source supports Watch AND the subscription path is in
// scope; InactiveReason carries a human-readable diagnostic when
// Active=false.
//
// Phase 1 always returns Active=false with InactiveReason set to
// a Phase-specific string — see config-go ADR-0001 §Watch.
type Subscription struct {
	// Active is true iff at least one registered source supports
	// Watch AND covers the subscription path. When false, the
	// callback is guaranteed never to fire.
	Active bool

	// InactiveReason is a human-readable diagnostic populated only
	// when Active=false. Examples from spec ADR-0001 §7.2:
	//
	//   "no watch-capable source registered"
	//   "no source covers this path"
	InactiveReason string

	// Path echoes the subscription path for introspection.
	Path string

	// unsubscribe is the registered cancellation function;
	// it is invoked by Unsubscribe and is idempotent.
	unsubscribe func()
}

// Unsubscribe cancels the subscription. It is idempotent — repeated
// calls are no-op. After Unsubscribe returns, the callback is
// guaranteed not to be invoked again, even for reload batches
// already in flight.
func (s *Subscription) Unsubscribe() {
	if s == nil || s.unsubscribe == nil {
		return
	}
	fn := s.unsubscribe
	s.unsubscribe = nil
	fn()
}

// ChangeEvent is delivered to Config.OnChange callbacks when the
// subscribed path changes. Spec ADR-0001 §7.2 structure.
type ChangeEvent struct {
	// Path is the dot-notation key that changed.
	Path string

	// OldValue is the value before the change, nil if the key was
	// absent.
	OldValue any

	// NewValue is the value after the change, nil if the key was
	// removed.
	NewValue any

	// SourceID is the ID of the Source that delivered the change.
	SourceID string

	// ChangeID is a monotonic identifier shared by all events in a
	// single reload batch — use it to coalesce notifications.
	ChangeID string

	// TimestampUnixNano is the time of the reload as Unix nanoseconds
	// (wire-stable per spec §1.2a; ISO-8601 rendering is a formatter
	// concern, not a field).
	TimestampUnixNano int64
}
