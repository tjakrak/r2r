package store

import (
	"testing"
	"time"

	"r2r/internal/control"
)

func TestSearchRunsFiltersAndPaginates(t *testing.T) {
	s := &Store{config: Config{
		Models: []control.Model{{FamilyID: "etch", FolderID: "area-a"}, {FamilyID: "polish", FolderID: "area-b"}},
		Runs: []RunRecord{
			{RunID: "one", FamilyID: "etch", Status: "completed", PreparedAt: "2026-10-01T10:00:00Z", Sequence: 1},
			{RunID: "two", FamilyID: "etch", Status: "prepared", PreparedAt: "2026-10-02T10:00:00Z", Sequence: 2},
			{RunID: "three", FamilyID: "polish", Status: "completed", PreparedAt: "2026-10-03T10:00:00Z", Sequence: 3},
		},
	}}
	filter := RunSearch{From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC), FolderID: "area-a", Limit: 1}
	first, err := s.SearchRuns(filter)
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 2 || len(first.Runs) != 1 || first.Runs[0].RunID != "two" {
		t.Fatalf("unexpected first page: %+v", first)
	}
	filter.Offset = 1
	second, err := s.SearchRuns(filter)
	if err != nil {
		t.Fatal(err)
	}
	if second.Total != 2 || len(second.Runs) != 1 || second.Runs[0].RunID != "one" {
		t.Fatalf("unexpected second page: %+v", second)
	}
	filter.Offset, filter.Limit, filter.Status = 0, 50, "completed"
	completed, err := s.SearchRuns(filter)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Total != 1 || completed.Runs[0].RunID != "one" {
		t.Fatalf("unexpected status filter: %+v", completed)
	}
	filter.FamilyIDs = []string{"polish"}
	empty, err := s.SearchRuns(filter)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Total != 0 {
		t.Fatalf("folder and model filters should intersect: %+v", empty)
	}
}
