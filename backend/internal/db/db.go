// Package db opens the SQLite database, applies migrations and exposes the
// sqlc-generated queries over a single-connection writer pool and a small
// read-only reader pool.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"sync/atomic"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // database/sql driver "sqlite"

	"filet/internal/db/gen"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB bundles the writer and reader pools.
type DB struct {
	Writer *sql.DB
	Reader *sql.DB
	// W runs queries on the writer pool; R on the reader pool.
	W *gen.Queries
	R *gen.Queries

	queries atomic.Int64
}

// Open opens path, applies pragmas and runs migrations.
func Open(ctx context.Context, path string) (*DB, error) {
	base := "file:" + (&url.URL{Path: path}).EscapedPath()
	pragmas := "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate"
	w, err := sql.Open("sqlite", base+pragmas)
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)
	if err := w.PingContext(ctx); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := migrate(ctx, w); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	r, err := sql.Open("sqlite", base+pragmas+"&_pragma=query_only(1)")
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(4)
	r.SetMaxIdleConns(4)
	d := &DB{Writer: w, Reader: r}
	d.W = gen.New(&counting{DBTX: w, n: &d.queries})
	d.R = gen.New(&counting{DBTX: r, n: &d.queries})
	return d, nil
}

func migrate(ctx context.Context, w *sql.DB) error {
	p, err := goose.NewProvider(goose.DialectSQLite3, w, mustSub(migrationsFS, "migrations"))
	if err != nil {
		return err
	}
	_, err = p.Up(ctx)
	return err
}

// Close closes both pools. Closing the last writer connection checkpoints
// the WAL.
func (d *DB) Close() error {
	_ = d.Reader.Close()
	_, _ = d.Writer.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return d.Writer.Close()
}

// QueryCount returns the number of sqlc queries executed so far. Tests use it
// to assert that a request never reached the database.
func (d *DB) QueryCount() int64 { return d.queries.Load() }

// Tx runs fn inside a write transaction.
func (d *DB) Tx(ctx context.Context, fn func(q *gen.Queries, tx *sql.Tx) error) error {
	tx, err := d.Writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(gen.New(&counting{DBTX: tx, n: &d.queries}), tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// InsertChunks bulk-inserts chunk hash rows inside tx with one prepared
// statement. This is the one hand-written statement allowed outside sqlc.
func InsertChunks(ctx context.Context, tx *sql.Tx, uploadID string, hashes []byte) error {
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO upload_chunks (upload_id, idx, hash) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for i := 0; i*32 < len(hashes); i++ {
		if _, err := stmt.ExecContext(ctx, uploadID, i, hashes[i*32:i*32+32]); err != nil {
			return err
		}
	}
	return nil
}

// Ping checks the database connection.
func (d *DB) Ping(ctx context.Context) error { return d.Reader.PingContext(ctx) }

type counting struct {
	gen.DBTX
	n *atomic.Int64
}

func (c *counting) ExecContext(ctx context.Context, q string, a ...any) (sql.Result, error) {
	c.n.Add(1)
	return c.DBTX.ExecContext(ctx, q, a...)
}

func (c *counting) QueryContext(ctx context.Context, q string, a ...any) (*sql.Rows, error) {
	c.n.Add(1)
	return c.DBTX.QueryContext(ctx, q, a...)
}

func (c *counting) QueryRowContext(ctx context.Context, q string, a ...any) *sql.Row {
	c.n.Add(1)
	return c.DBTX.QueryRowContext(ctx, q, a...)
}

func mustSub(f embed.FS, dir string) fs.FS {
	s, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return s
}
