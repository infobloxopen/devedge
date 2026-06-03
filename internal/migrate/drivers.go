// Package migrate is devedge's portable schema-migration and dev-seed layer.
//
// It wraps the Infoblox golang-migrate fork (github.com/infobloxopen/migrate,
// branch ib), pulled in via the go.mod replace of github.com/golang-migrate/migrate/v4.
// The fork's persisted down-migration store and dirty-state recovery (PR #57) deliver
// rollback-survives-image-change (FR-012) and corrected-re-run recovery (FR-007) without
// devedge re-implementing them. Code here imports the standard golang-migrate surface;
// the replace makes the fork transparent.
//
// Migration and seed logic lives behind a portable Applier (Constitution IV); the engine
// and its drivers are registered here, the abstraction and fork-backed implementation
// alongside in this package.
package migrate

import (
	// Database driver: Postgres over pgx/v5 (the per-service isolated DB from 003).
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	// Source drivers: local migration files (local-run) and embedded io/fs trees.
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/golang-migrate/migrate/v4/source/iofs"
)
