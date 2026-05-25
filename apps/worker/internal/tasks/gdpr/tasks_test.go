package gdpr

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/jobs/taskspec"
)

type fakeExportStore struct {
	url      string
	assembleErr error
	markErr  error

	gotUserID, gotJobID string
	markedJobID, markedURL string
}

func (f *fakeExportStore) AssembleExport(_ context.Context, userID, jobID string) (string, error) {
	f.gotUserID = userID
	f.gotJobID = jobID
	return f.url, f.assembleErr
}

func (f *fakeExportStore) MarkExportReady(_ context.Context, jobID, url string) error {
	f.markedJobID = jobID
	f.markedURL = url
	return f.markErr
}

type fakePurgeStore struct {
	ids    []string
	err    error
	deleted []string
	deleteErrs map[string]error
}

func (f *fakePurgeStore) SelectDuePurges(_ context.Context, _ time.Time, _ int) ([]string, error) {
	return f.ids, f.err
}

func (f *fakePurgeStore) HardDelete(_ context.Context, id string) error {
	if f.deleteErrs != nil {
		if err, ok := f.deleteErrs[id]; ok {
			return err
		}
	}
	f.deleted = append(f.deleted, id)
	return nil
}

func TestExportHandler_HappyPath(t *testing.T) {
	store := &fakeExportStore{url: "https://store.example.com/exports/abc.zip"}
	specs := Specs(Deps{Exports: store, Purges: &fakePurgeStore{}})

	var spec = findSpec(t, specs, TaskExportRun)
	payload, _ := json.Marshal(ExportPayload{UserID: "u-1", JobID: "j-1"})
	if err := spec.Handler(context.Background(), payload); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if store.gotUserID != "u-1" || store.gotJobID != "j-1" {
		t.Errorf("assemble called with (%q,%q), want (u-1,j-1)", store.gotUserID, store.gotJobID)
	}
	if store.markedURL != store.url || store.markedJobID != "j-1" {
		t.Errorf("mark ready called with (%q,%q)", store.markedJobID, store.markedURL)
	}
}

func TestExportHandler_MissingFields(t *testing.T) {
	specs := Specs(Deps{Exports: &fakeExportStore{}, Purges: &fakePurgeStore{}})
	spec := findSpec(t, specs, TaskExportRun)

	payload, _ := json.Marshal(ExportPayload{UserID: "", JobID: "j-1"})
	if err := spec.Handler(context.Background(), payload); err == nil {
		t.Error("expected error on empty user_id")
	}
}

func TestPurgeHandler_DeletesEachId(t *testing.T) {
	store := &fakePurgeStore{ids: []string{"u-1", "u-2", "u-3"}}
	specs := Specs(Deps{Exports: &fakeExportStore{}, Purges: store})
	spec := findSpec(t, specs, TaskPurgeTick)

	if err := spec.Handler(context.Background(), nil); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if len(store.deleted) != 3 {
		t.Errorf("deleted = %v, want 3 items", store.deleted)
	}
}

func TestPurgeHandler_DryRun(t *testing.T) {
	store := &fakePurgeStore{ids: []string{"u-1"}}
	specs := Specs(Deps{Exports: &fakeExportStore{}, Purges: store})
	spec := findSpec(t, specs, TaskPurgeTick)

	payload, _ := json.Marshal(PurgePayload{DryRun: true})
	if err := spec.Handler(context.Background(), payload); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if len(store.deleted) != 0 {
		t.Errorf("dry run must not delete; got %v", store.deleted)
	}
}

func TestPurgeHandler_PartialFailureReturnsError(t *testing.T) {
	store := &fakePurgeStore{
		ids: []string{"u-1", "u-2"},
		deleteErrs: map[string]error{"u-2": errors.New("boom")},
	}
	specs := Specs(Deps{Exports: &fakeExportStore{}, Purges: store})
	spec := findSpec(t, specs, TaskPurgeTick)

	if err := spec.Handler(context.Background(), nil); err == nil {
		t.Error("expected error on partial failure")
	}
	if len(store.deleted) != 1 || store.deleted[0] != "u-1" {
		t.Errorf("deleted = %v, want [u-1]", store.deleted)
	}
}

func TestCronSpec(t *testing.T) {
	s := CronSpec()
	if s.Name != PurgeCronName {
		t.Errorf("Name = %q", s.Name)
	}
	if s.Schedule != PurgeCronSchedule {
		t.Errorf("Schedule = %q", s.Schedule)
	}
	if s.TaskName != TaskPurgeTick {
		t.Errorf("TaskName = %q", s.TaskName)
	}
}

// findSpec returns the TaskSpec with the given name. Fails the test
// if not found — a missing spec is a wiring bug we want to surface
// loudly.
func findSpec(t *testing.T, specs []taskspec.TaskSpec, name string) taskspec.TaskSpec {
	t.Helper()
	for _, s := range specs {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("spec %q not found in %d specs", name, len(specs))
	return taskspec.TaskSpec{}
}
