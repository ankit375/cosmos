package postgres

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pooler is an interface for database operations — enables testing with mocks.
type pooler interface {
	Exec(ctx context.Context, sql string, arguments ...interface{}) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Ensure pgxpool.Pool satisfies the pooler interface.
var _ pooler = (*pgxpool.Pool)(nil)

// joinStrings joins string slice with a separator.
func joinStrings(elems []string, sep string) string {
	return strings.Join(elems, sep)
}

// IsUniqueViolation checks if the error is a PostgreSQL unique constraint violation.
func IsUniqueViolation(err error) bool {
	if pgErr, ok := err.(*pgconn.PgError); ok {
		return pgErr.Code == "23505"
	}
	return false
}

// IsForeignKeyViolation checks if the error is a PostgreSQL foreign key violation.
func IsForeignKeyViolation(err error) bool {
	if pgErr, ok := err.(*pgconn.PgError); ok {
		return pgErr.Code == "23503"
	}
	return false
}
