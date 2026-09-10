// Package employeedirectory provides access to the corporate employee directory.
package employeedirectory

import (
	"context"
	"database/sql"
	"strings"
)

// Record contains the corporate identity fields used by R2R.
type Record struct {
	IDNum        string `json:"IDNUM"`
	LoginID      string `json:"LOGINID"`
	FirstName    string `json:"FIRSTNAME"`
	LastName     string `json:"LASTNAME"`
	Email        string `json:"EMAIL"`
	SupervisorID string `json:"SUPID"`
}

// DisplayName returns the employee's full name, falling back to the login ID
// when the directory does not provide either name component.
func (r Record) DisplayName() string {
	name := strings.TrimSpace(strings.TrimSpace(r.FirstName) + " " + strings.TrimSpace(r.LastName))
	if name == "" {
		return strings.TrimSpace(r.LoginID)
	}
	return name
}

// Repository describes the employee-directory lookups required by R2R.
type Repository interface {
	ByLoginID(ctx context.Context, loginID string) (Record, error)
	ByEmployeeID(ctx context.Context, employeeID string) (Record, error)
}

// SQLRepository reads employees from the company's SQL Server directory view.
type SQLRepository struct {
	db *sql.DB
}

// NewSQLRepository creates a repository backed by an existing SQL connection.
func NewSQLRepository(db *sql.DB) *SQLRepository {
	return &SQLRepository{db: db}
}

const byLoginIDQuery = `
	SELECT IDNUM, LOGINID, COALESCE(FIRSTNAME, N''), COALESCE(LASTNAME, N''), COALESCE(EMAIL, N''), COALESCE(SUPID, N'')
	FROM dbo.HDWY_INTRANET_USERS
	WHERE LOGINID = @p1 AND ENABLED = N'TRUE'`

const byEmployeeIDQuery = `
	SELECT IDNUM, LOGINID, COALESCE(FIRSTNAME, N''), COALESCE(LASTNAME, N''), COALESCE(EMAIL, N''), COALESCE(SUPID, N'')
	FROM dbo.HDWY_INTRANET_USERS
	WHERE IDNUM = @p1 AND ENABLED = N'TRUE'`

// ByLoginID returns the employee with the supplied corporate login ID.
func (r *SQLRepository) ByLoginID(ctx context.Context, loginID string) (Record, error) {
	return r.queryOne(ctx, byLoginIDQuery, loginID)
}

// ByEmployeeID returns the employee with the supplied corporate employee ID.
func (r *SQLRepository) ByEmployeeID(ctx context.Context, employeeID string) (Record, error) {
	return r.queryOne(ctx, byEmployeeIDQuery, employeeID)
}

func (r *SQLRepository) queryOne(ctx context.Context, query, value string) (Record, error) {
	var employee Record
	err := r.db.QueryRowContext(ctx, query, strings.TrimSpace(value)).Scan(
		&employee.IDNum,
		&employee.LoginID,
		&employee.FirstName,
		&employee.LastName,
		&employee.Email,
		&employee.SupervisorID,
	)
	if err != nil {
		return Record{}, err
	}
	employee.IDNum = strings.TrimSpace(employee.IDNum)
	employee.LoginID = strings.TrimSpace(employee.LoginID)
	employee.FirstName = strings.TrimSpace(employee.FirstName)
	employee.LastName = strings.TrimSpace(employee.LastName)
	employee.Email = strings.TrimSpace(employee.Email)
	employee.SupervisorID = strings.TrimSpace(employee.SupervisorID)
	return employee, nil
}
