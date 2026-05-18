package cron

import (
	"errors"
	"testing"
)

// TestSeedDefaults_RegistersRevisionsPurge confirms the seed entry
// lands in the registry and uses the documented schedule + task name.
// The test pins the constants so a future rename to one of the seed
// entries shows up here too.
func TestSeedDefaults_RegistersRevisionsPurge(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	if err := SeedDefaults(reg); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}
	got, ok := reg.Get(ScheduleRevisionsPurgeDaily)
	if !ok {
		t.Fatalf("expected %q to be registered", ScheduleRevisionsPurgeDaily)
	}
	if got.TaskName != TaskNameRevisionsPurge {
		t.Errorf("TaskName: got %q, want %q", got.TaskName, TaskNameRevisionsPurge)
	}
	if got.Schedule != "0 3 * * *" {
		t.Errorf("Schedule: got %q, want %q", got.Schedule, "0 3 * * *")
	}
}

// TestSeedDefaults_NilRegistryRejected covers the nil-registry
// nuisance path: a misconfigured boot must surface this immediately.
func TestSeedDefaults_NilRegistryRejected(t *testing.T) {
	t.Parallel()
	if err := SeedDefaults(nil); err == nil {
		t.Fatal("SeedDefaults(nil): want error")
	}
}

// TestSeedDefaults_DoubleCallSurfacesError covers the duplicate
// path: calling SeedDefaults twice must propagate ErrAlreadyRegistered
// so the wiring bug shows up cleanly.
func TestSeedDefaults_DoubleCallSurfacesError(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	if err := SeedDefaults(reg); err != nil {
		t.Fatalf("first SeedDefaults: %v", err)
	}
	err := SeedDefaults(reg)
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("second SeedDefaults: got %v, want ErrAlreadyRegistered", err)
	}
}
