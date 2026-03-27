package kanban

import (
	"net/url"
	"testing"
)

func TestSQLiteDSN(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "unix path",
			path: "/tmp/maestro.db",
			want: "/tmp/maestro.db",
		},
		{
			name: "windows drive path",
			path: "C:/Users/olhapi/maestro.db",
			want: "/C:/Users/olhapi/maestro.db",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sqliteDSN(tc.path)
			u, err := url.Parse(got)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", got, err)
			}
			if u.Scheme != "file" {
				t.Fatalf("scheme = %q, want %q", u.Scheme, "file")
			}
			if u.Path != tc.want {
				t.Fatalf("path = %q, want %q", u.Path, tc.want)
			}
			if u.Query().Get("_txlock") != "immediate" {
				t.Fatalf("missing txlock query in %q", got)
			}
		})
	}
}
