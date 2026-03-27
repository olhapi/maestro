package kanban

import (
	"database/sql"
	"net/url"
	"path/filepath"

	moderncsqlite "modernc.org/sqlite"
)

func init() {
	// Preserve the historical sqlite3 driver name so existing call sites stay stable.
	sql.Register("sqlite3", &moderncsqlite.Driver{})
}

func sqliteDSN(dbPath string) string {
	path := filepath.ToSlash(dbPath)
	if isWindowsDrivePath(path) {
		path = "/" + path
	}

	u := url.URL{
		Scheme: "file",
		Path:   path,
	}

	q := u.Query()
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()

	return u.String()
}

func isWindowsDrivePath(path string) bool {
	return len(path) >= 2 &&
		path[1] == ':' &&
		((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z'))
}
