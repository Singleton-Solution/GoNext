package fakehost

import (
	"fmt"
)

// Platform ABI surface (gn_secrets_get, gn_audit_emit, gn_cron_register).

// SecretsGet returns the value seeded via SetSecret. Returns
// ErrNotFound if the secret has not been seeded.
//
// Capability gating: requires "secrets".
func (h *Host) SecretsGet(name string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := map[string]any{"name": name}
	if err := h.requireCapLocked(EventSecretsGet, "secrets", args); err != nil {
		return "", err
	}
	v, ok := h.secrets[name]
	if !ok {
		h.recordLocked(EventSecretsGet, args, nil)
		return "", fmt.Errorf("%w: secret %s", ErrNotFound, name)
	}
	// We do NOT include the secret value in the recorded event by
	// default — leaking secrets into test fixtures is exactly the
	// kind of mistake the conformance suite should help avoid. The
	// caller-visible return value is the real value; the trace
	// shows only the name.
	h.recordLocked(EventSecretsGet, args, map[string]any{"hit": true})
	return v, nil
}

// AuditEmit records an audit event. The fake host does not persist
// audit rows — it appends to the event trace under EventAuditEmit so
// scenarios can assert "the plugin emitted audit event X with
// metadata Y".
//
// Capability gating: requires "audit.emit".
func (h *Host) AuditEmit(eventType string, metadata map[string]any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := map[string]any{
		"event_type": eventType,
		"metadata":   cloneFields(metadata),
		"slug":       h.slug,
	}
	if err := h.requireCapLocked(EventAuditEmit, "audit.emit", args); err != nil {
		return err
	}
	h.recordLocked(EventAuditEmit, args, nil)
	return nil
}

// CronRegister installs a scheduled job under the given name. The
// fake host does not actually schedule anything — scenarios can
// assert that the registration happened with the right spec, and
// can drive the handler manually if they need to simulate a tick.
//
// Capability gating: requires "cron".
func (h *Host) CronRegister(name, spec, handler string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := map[string]any{
		"name":    name,
		"spec":    spec,
		"handler": handler,
	}
	if err := h.requireCapLocked(EventCronRegister, "cron", args); err != nil {
		return err
	}
	h.recordLocked(EventCronRegister, args, nil)
	return nil
}
