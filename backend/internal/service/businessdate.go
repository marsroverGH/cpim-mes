package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"
	_ "time/tzdata" // the runtime image has no zoneinfo; embed it

	"github.com/jmoiron/sqlx"
)

// Business dates
//
// Planning works on calendar days of the business, not on server-local days.
// PostgreSQL already defines the business day with eco_business_timezone()
// (session GUC app.business_timezone, fallback Asia/Tokyo). The Go side must
// agree with it no matter which TZ the server process runs in — the backend
// container runs in UTC, so between 00:00 and 09:00 JST "today" used to be the
// previous day in Go but not in the database.
//
// Canonical form: a business date is a time.Time at 00:00 UTC whose
// year/month/day is the business calendar day. That is also how pgx returns
// DATE columns, so values from the database and values computed in Go compare
// and hash (as map keys) consistently.

// BusinessTimezoneEnv overrides the business timezone (IANA name). It must
// match the database's app.business_timezone setting when that is changed.
const BusinessTimezoneEnv = "BUSINESS_TIMEZONE"

const defaultBusinessTimezone = "Asia/Tokyo"

var businessLoc atomic.Pointer[time.Location]

func init() {
	name := strings.TrimSpace(os.Getenv(BusinessTimezoneEnv))
	if name == "" {
		name = defaultBusinessTimezone
	}
	if err := SetBusinessTimezone(name); err != nil {
		panic(err)
	}
}

// SetBusinessTimezone sets the timezone that defines business calendar days.
func SetBusinessTimezone(name string) error {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return fmt.Errorf("invalid %s %q: %w", BusinessTimezoneEnv, name, err)
	}
	businessLoc.Store(loc)
	return nil
}

// BusinessLocation returns the timezone that defines business calendar days.
func BusinessLocation() *time.Location { return businessLoc.Load() }

// CheckBusinessTimezone returns an error when the Go business timezone and the
// database's eco_business_timezone() differ. Business dates would otherwise
// silently disagree between Go and the PL/pgSQL rules during part of each day.
func CheckBusinessTimezone(ctx context.Context, db *sqlx.DB) error {
	return checkBusinessTimezone(ctx, db, BusinessLocation().String())
}

func checkBusinessTimezone(ctx context.Context, db *sqlx.DB, goTimezone string) error {
	var dbTimezone string
	if err := db.GetContext(ctx, &dbTimezone, `SELECT eco_business_timezone()`); err != nil {
		return fmt.Errorf("read database business timezone: %w", err)
	}
	// PostgreSQL matches timezone names case-insensitively. Aliases such as
	// Japan and Asia/Tokyo are deliberately treated as different: one name on
	// both sides is simple to verify.
	if !strings.EqualFold(goTimezone, dbTimezone) {
		return fmt.Errorf("%s is %q but the database app.business_timezone is %q; "+
			"set both to the same IANA name (ALTER DATABASE <db> SET app.business_timezone = '<name>')",
			BusinessTimezoneEnv, goTimezone, dbTimezone)
	}
	return nil
}

// nowFunc is replaced in tests.
var nowFunc = time.Now

// BusinessToday returns today's business date in canonical form.
func BusinessToday() time.Time { return BusinessDate(nowFunc()) }

// BusinessDate converts t to a canonical business date.
//
//   - A value at exactly midnight in its own location is a date value (a DATE
//     column, or a request field such as "2027-03-01T00:00:00Z" or
//     "2027-03-01T00:00:00+09:00"); its year/month/day label is kept.
//   - Any other value is an instant; it is converted to the business timezone
//     and that calendar day is used.
//
// The zero time stays zero.
func BusinessDate(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	if h, m, s := t.Clock(); h != 0 || m != 0 || s != 0 || t.Nanosecond() != 0 {
		t = t.In(BusinessLocation())
	}
	y, mo, d := t.Date()
	return time.Date(y, mo, d, 0, 0, 0, 0, time.UTC)
}

// ParseDateOnly parses a calendar-date input. It accepts both the date-only
// form (2026-11-11) and RFC3339 (2026-11-11T00:00:00Z, the format used by the
// rest of the API) and returns the business date in canonical form (UTC
// midnight). An RFC3339 instant is read in the business timezone, like every
// other instant in the system.
func ParseDateOnly(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("date must be YYYY-MM-DD or RFC3339: %q", raw)
	}
	return BusinessDate(t), nil
}
