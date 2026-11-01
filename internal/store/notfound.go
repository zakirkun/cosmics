package store

import (
	"database/sql"
	"errors"
)

// isNoRows reports whether the error is the standard empty-result signal.
//
// Scan helpers wrap sql.ErrNoRows on its way up, so a direct comparison would
// miss it and every "not found" would look like a hard failure.
func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
