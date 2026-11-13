//go:build !pg

package store

import "errors"

// NewPostgres is available only in builds with -tags pg (pulls in pgx).
func NewPostgres(dsn string) (Store, error) {
	return nil, errors.New("postgres store: rebuild with -tags pg (see docs/deployment.md)")
}
