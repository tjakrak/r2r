package store

import "testing"

func TestSearchUserRegistrationsFiltersAndPaginates(t *testing.T) {
	s := &Store{config: Config{Users: []UserRegistration{
		{EmployeeID: "E2", LoginID: "zebra", DisplayName: "Zebra Example", Email: "z@example.com", Role: "viewer", Status: "active"},
		{EmployeeID: "E1", LoginID: "alpha", DisplayName: "Alice Example", Email: "a@example.com", Role: "engineer", Status: "active"},
		{EmployeeID: "E3", LoginID: "beta", DisplayName: "Bob Example", Email: "b@example.com", Role: "engineer", Status: "disabled"},
	}}}
	first, err := s.SearchUserRegistrations(UserSearch{Page: 1, PageSize: 2})
	if err != nil || first.Total != 3 || len(first.Items) != 2 || first.Items[0].EmployeeID != "E1" || first.Items[1].EmployeeID != "E3" {
		t.Fatalf("unexpected first page: %+v, %v", first, err)
	}
	second, err := s.SearchUserRegistrations(UserSearch{Page: 2, PageSize: 2})
	if err != nil || second.Total != 3 || len(second.Items) != 1 || second.Items[0].EmployeeID != "E2" {
		t.Fatalf("unexpected second page: %+v, %v", second, err)
	}
	filtered, err := s.SearchUserRegistrations(UserSearch{Query: "EXAMPLE", Role: "engineer", Status: "disabled", Page: 1, PageSize: 25})
	if err != nil || filtered.Total != 1 || filtered.Items[0].EmployeeID != "E3" {
		t.Fatalf("unexpected filtered users: %+v, %v", filtered, err)
	}
}

func TestSearchUserRegistrationsRejectsInvalidPage(t *testing.T) {
	s := &Store{}
	for _, filter := range []UserSearch{
		{Page: 0, PageSize: 25}, {Page: 1, PageSize: 101}, {Page: 1, PageSize: 25, Role: "superuser"},
	} {
		if _, err := s.SearchUserRegistrations(filter); err == nil {
			t.Fatalf("expected validation error for %+v", filter)
		}
	}
}
