// Package dbutil provides small shared helpers for database operations.
package dbutil

import (
	"database/sql"
	"fmt"
)

// CheckAffected returns notFound if the UPDATE/DELETE matched no rows,
// or wraps the database error from RowsAffected.
func CheckAffected(res sql.Result, id int64, notFound error) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking update result for id %d: %w", id, err)
	}
	if n == 0 {
		return notFound
	}
	return nil
}
