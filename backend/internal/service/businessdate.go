package service

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"
	_ "time/tzdata" // the runtime image has no zoneinfo; embed it
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
