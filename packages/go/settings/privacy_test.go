package settings

import (
	"context"
	"testing"
)

func TestRegisterPrivacy_AllRegister(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterPrivacy(reg); err != nil {
		t.Fatalf("RegisterPrivacy: %v", err)
	}
	store := NewMemoryStore(reg)
	for _, s := range PrivacySettings() {
		v, err := store.Read(context.Background(), s.Key)
		if err != nil {
			t.Errorf("Read %s: %v", s.Key, err)
			continue
		}
		if v == nil && s.Default != nil {
			t.Errorf("expected default for %s, got nil", s.Key)
		}
	}
}

func TestPrivacyRetentionAcceptsInteger(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterPrivacy(reg); err != nil {
		t.Fatalf("RegisterPrivacy: %v", err)
	}
	store := NewMemoryStore(reg)
	// 0 means "retain forever" — schema allows it.
	if err := store.Write(context.Background(), PrivacyRetentionAuditDays, float64(0)); err != nil {
		t.Fatalf("Write 0: %v", err)
	}
	if err := store.Write(context.Background(), PrivacyRetentionAuditDays, float64(180)); err != nil {
		t.Fatalf("Write 180: %v", err)
	}
}

func TestPrivacyAllowGDPRSelfServiceBoolean(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterPrivacy(reg); err != nil {
		t.Fatalf("RegisterPrivacy: %v", err)
	}
	store := NewMemoryStore(reg)
	if err := store.Write(context.Background(), PrivacyAllowGDPRSelfService, false); err != nil {
		t.Fatalf("Write false: %v", err)
	}
	v, _ := store.Read(context.Background(), PrivacyAllowGDPRSelfService)
	if v.(bool) != false {
		t.Fatalf("expected false, got %v", v)
	}
}
