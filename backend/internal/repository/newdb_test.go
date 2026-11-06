package repository_test

import (
	"os"
	"testing"

	"github.com/cpim-mes/backend/internal/repository"
	"github.com/cpim-mes/backend/internal/testdb"
)

func TestNewDB(t *testing.T) {
	t.Parallel()
	// An unreachable server must fail at Ping rather than returning a lazy pool.
	if db, err := repository.NewDB("postgres://nobody:x@127.0.0.1:1/none?sslmode=disable&connect_timeout=2"); err == nil {
		_ = db.Close()
		t.Fatal("expected ping error for unreachable database")
	}
	if _, err := repository.NewDB("://not a url"); err == nil {
		t.Fatal("expected error for malformed DSN")
	}

	url := os.Getenv(testdb.EnvVar)
	if url == "" {
		t.Skipf("%s is not set", testdb.EnvVar)
	}
	db, err := repository.NewDB(url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := db.Stats().MaxOpenConnections; got != 20 {
		t.Fatalf("MaxOpenConnections = %d, want 20", got)
	}
}
