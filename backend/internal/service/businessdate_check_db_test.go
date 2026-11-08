package service

import (
	"context"
	"strings"
	"testing"

	"github.com/cpim-mes/backend/internal/testdb"
)

func TestCheckBusinessTimezoneComparesWithDatabase(t *testing.T) {
	db := testdb.New(t)
	db.SetMaxOpenConns(1) // SET below applies to the only session.
	ctx := context.Background()

	setDB := func(zone string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `SELECT set_config('app.business_timezone', $1, false)`, zone); err != nil {
			t.Fatal(err)
		}
	}

	if err := checkBusinessTimezone(ctx, db, "Asia/Tokyo"); err != nil {
		t.Fatalf("both defaults must match: %v", err)
	}
	setDB("asia/tokyo")
	if err := checkBusinessTimezone(ctx, db, "Asia/Tokyo"); err != nil {
		t.Fatalf("names differing only by case must match: %v", err)
	}
	setDB("UTC")
	err := checkBusinessTimezone(ctx, db, "Asia/Tokyo")
	if err == nil || !strings.Contains(err.Error(), `"Asia/Tokyo"`) || !strings.Contains(err.Error(), `"UTC"`) {
		t.Fatalf("mismatch error = %v", err)
	}
	setDB("Mars/Olympus_Mons")
	if err := checkBusinessTimezone(ctx, db, "Asia/Tokyo"); err == nil {
		t.Fatal("an invalid database timezone must fail the check")
	}
}
