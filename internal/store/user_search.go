package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type UserSearch struct {
	Query    string
	Role     string
	Status   string
	Page     int
	PageSize int
}

type UserSearchResult struct {
	Items    []UserRegistration `json:"items"`
	Total    int                `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"pageSize"`
}

var ErrInvalidUserSearch = errors.New("invalid user search filters or page")

func (s *Store) SearchUserRegistrations(filter UserSearch) (UserSearchResult, error) {
	filter.Query = strings.TrimSpace(filter.Query)
	if filter.Page < 1 || filter.Page > 100000 || filter.PageSize < 1 || filter.PageSize > 100 ||
		len([]rune(filter.Query)) > 200 ||
		(filter.Role != "" && filter.Role != "viewer" && filter.Role != "engineer" && filter.Role != "engineering_manager" && filter.Role != "admin") ||
		(filter.Status != "" && filter.Status != "active" && filter.Status != "disabled" && filter.Status != "locked") {
		return UserSearchResult{}, ErrInvalidUserSearch
	}
	if s.db != nil {
		return s.searchUsersSQL(filter)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	matched := make([]UserRegistration, 0)
	needle := strings.ToLower(filter.Query)
	for _, registration := range s.config.Users {
		if filter.Role != "" && registration.Role != filter.Role || filter.Status != "" && registration.Status != filter.Status {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(registration.EmployeeID+" "+registration.LoginID+" "+registration.DisplayName+" "+registration.Email), needle) {
			continue
		}
		matched = append(matched, registration)
	}
	sort.Slice(matched, func(i, j int) bool {
		a, b := strings.ToLower(matched[i].LoginID), strings.ToLower(matched[j].LoginID)
		if a == b {
			return matched[i].EmployeeID < matched[j].EmployeeID
		}
		return a < b
	})
	result := UserSearchResult{Items: []UserRegistration{}, Total: len(matched), Page: filter.Page, PageSize: filter.PageSize}
	start := (filter.Page - 1) * filter.PageSize
	if start < len(matched) {
		end := start + filter.PageSize
		if end > len(matched) {
			end = len(matched)
		}
		result.Items = append(result.Items, matched[start:end]...)
	}
	return result, nil
}

func (s *Store) searchUsersSQL(filter UserSearch) (UserSearchResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	args := []interface{}{}
	where := []string{}
	if filter.Query != "" {
		args = append(args, strings.ToLower(filter.Query))
		p := fmt.Sprintf("@p%d", len(args))
		where = append(where, "(CHARINDEX("+p+", LOWER(U.EMPLOYEE_ID)) > 0 OR CHARINDEX("+p+", LOWER(U.LOGIN_NAME)) > 0 OR CHARINDEX("+p+", LOWER(U.DISPLAY_NAME)) > 0 OR CHARINDEX("+p+", LOWER(COALESCE(U.EMAIL_ADDRESS,N''))) > 0)")
	}
	if filter.Role != "" {
		args = append(args, filter.Role)
		where = append(where, fmt.Sprintf("U.ROLE_NAME = @p%d", len(args)))
	}
	if filter.Status != "" {
		args = append(args, filter.Status)
		where = append(where, fmt.Sprintf("U.STATUS = @p%d", len(args)))
	}
	clause := " FROM PCS.USERS U"
	if len(where) > 0 {
		clause += " WHERE " + strings.Join(where, " AND ")
	}
	result := UserSearchResult{Items: []UserRegistration{}, Page: filter.Page, PageSize: filter.PageSize}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT_BIG(*)"+clause, args...).Scan(&result.Total); err != nil {
		return UserSearchResult{}, err
	}
	start := (filter.Page - 1) * filter.PageSize
	if start >= result.Total {
		return result, nil
	}
	pageArgs := append(append([]interface{}{}, args...), start, filter.PageSize)
	query := "SELECT U.EMPLOYEE_ID,U.LOGIN_NAME,U.DISPLAY_NAME,COALESCE(U.EMAIL_ADDRESS,N''),U.ROLE_NAME,U.STATUS" + clause +
		fmt.Sprintf(" ORDER BY U.LOGIN_NAME,U.EMPLOYEE_ID OFFSET @p%d ROWS FETCH NEXT @p%d ROWS ONLY", len(pageArgs)-1, len(pageArgs))
	rows, err := s.db.QueryContext(ctx, query, pageArgs...)
	if err != nil {
		return UserSearchResult{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var registration UserRegistration
		if err := rows.Scan(&registration.EmployeeID, &registration.LoginID, &registration.DisplayName, &registration.Email, &registration.Role, &registration.Status); err != nil {
			return UserSearchResult{}, err
		}
		result.Items = append(result.Items, registration)
	}
	if err := rows.Err(); err != nil {
		return UserSearchResult{}, err
	}
	return result, nil
}
