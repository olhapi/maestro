package kanban

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	moderncsqlite "modernc.org/sqlite"
)

var faultySQLiteDriverSeq int64

type failingSQLiteDriver struct {
	inner       driver.Driver
	failPattern string
	failErr     error
	failed      int32
}

func (d *failingSQLiteDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &failingSQLiteConn{
		Conn:        conn,
		failPattern: strings.ToLower(strings.TrimSpace(d.failPattern)),
		failErr:     d.failErr,
		failed:      &d.failed,
	}, nil
}

type failingSQLiteConn struct {
	driver.Conn
	failPattern string
	failErr     error
	failed      *int32
}

func (c *failingSQLiteConn) shouldFail(query string) bool {
	if c.failPattern == "" || c.failErr == nil {
		return false
	}
	if !strings.Contains(strings.ToLower(query), c.failPattern) {
		return false
	}
	return atomic.CompareAndSwapInt32(c.failed, 0, 1)
}

func (c *failingSQLiteConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c.shouldFail(query) {
		return nil, c.failErr
	}
	execer, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return execer.ExecContext(ctx, query, args)
}

func (c *failingSQLiteConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.shouldFail(query) {
		return nil, c.failErr
	}
	queryer, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return queryer.QueryContext(ctx, query, args)
}

func (c *failingSQLiteConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if c.failPattern == "__begin__" && atomic.CompareAndSwapInt32(c.failed, 0, 1) {
		return nil, c.failErr
	}
	beginner, ok := c.Conn.(driver.ConnBeginTx)
	if !ok {
		return c.Conn.Begin()
	}
	return beginner.BeginTx(ctx, opts)
}

func (c *failingSQLiteConn) Ping(ctx context.Context) error {
	pinger, ok := c.Conn.(driver.Pinger)
	if !ok {
		return nil
	}
	return pinger.Ping(ctx)
}

func openFaultySQLiteStore(t *testing.T, failPattern string) *Store {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "faulty.db")
	return openFaultySQLiteStoreAt(t, dbPath, failPattern)
}

func openFaultySQLiteStoreAt(t *testing.T, dbPath, failPattern string) *Store {
	t.Helper()

	driverName := fmt.Sprintf("sqlite3-faulty-%d", atomic.AddInt64(&faultySQLiteDriverSeq, 1))
	sql.Register(driverName, &failingSQLiteDriver{
		inner:       &moderncsqlite.Driver{},
		failPattern: failPattern,
		failErr:     errors.New("injected sqlite failure"),
	})

	db, err := sql.Open(driverName, sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}

	store := &Store{db: db, dbPath: dbPath}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return store
}

func openFaultyMigratedSQLiteStoreAt(t *testing.T, dbPath, failPattern string) *Store {
	t.Helper()

	store := openFaultySQLiteStoreAt(t, dbPath, failPattern)
	if err := store.configureConnection(); err != nil {
		t.Fatalf("configureConnection: %v", err)
	}
	if err := store.migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store
}

func openSQLiteStoreAt(t *testing.T, dbPath string) *Store {
	t.Helper()

	db, err := sql.Open("sqlite3", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	store := &Store{db: db, dbPath: dbPath}
	if err := store.configureConnection(); err != nil {
		_ = db.Close()
		t.Fatalf("configureConnection: %v", err)
	}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return store
}
