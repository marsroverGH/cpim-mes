package service

import (
	"context"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/google/uuid"
)

// ====================================================================
// Calendar Service
// ====================================================================

type CalendarService struct {
	r *repository.CalendarRepo
}

func (s *CalendarService) List(ctx context.Context) ([]domain.WorkCalendar, error) {
	return s.r.List(ctx)
}
func (s *CalendarService) Get(ctx context.Context, id uuid.UUID) (*domain.WorkCalendar, error) {
	return s.r.Get(ctx, id)
}
func (s *CalendarService) Default(ctx context.Context) (*domain.WorkCalendar, error) {
	return s.r.Default(ctx)
}
func (s *CalendarService) Create(ctx context.Context, c *domain.WorkCalendar) error {
	return s.r.Create(ctx, c)
}
func (s *CalendarService) Update(ctx context.Context, c *domain.WorkCalendar) error {
	return s.r.Update(ctx, c)
}
func (s *CalendarService) Delete(ctx context.Context, id uuid.UUID) error {
	return s.r.Delete(ctx, id)
}
func (s *CalendarService) Exceptions(ctx context.Context, calID uuid.UUID) ([]domain.CalendarException, error) {
	return s.r.Exceptions(ctx, calID)
}
func (s *CalendarService) AddException(ctx context.Context, e *domain.CalendarException) error {
	return s.r.AddException(ctx, e)
}
func (s *CalendarService) DeleteException(ctx context.Context, id uuid.UUID) error {
	return s.r.DeleteException(ctx, id)
}

// ====================================================================
// Pure helpers (testable)
// ====================================================================

// CalendarSnapshot — 期間内のカレンダーを高速参照するためのスナップショット
type CalendarSnapshot struct {
	Calendar   domain.WorkCalendar
	Exceptions map[time.Time]domain.CalendarException // key: TruncateDay(date)
}

// MinutesAvailable — 指定日に作業可能な分数を返す。
//  1. 例外があればそれを優先 (HOLIDAY → 0, WORKDAY → exception.Minutes)
//  2. 例外なしなら標準週次パターンの該当曜日を使用
func (cs CalendarSnapshot) MinutesAvailable(d time.Time) int {
	day := TruncateDay(d)
	if ex, ok := cs.Exceptions[day]; ok {
		if ex.Kind == "HOLIDAY" {
			return 0
		}
		return ex.Minutes
	}
	return cs.Calendar.MinutesForWeekday(day.Weekday())
}

// IsWorkDay — その日に少しでも稼働時間があれば true
func (cs CalendarSnapshot) IsWorkDay(d time.Time) bool {
	return cs.MinutesAvailable(d) > 0
}

// PreviousWorkDay — d 以前で最初に稼働日となる日を返す。
// d 自体が稼働日ならそのまま返す。連続休日の場合は最大 maxLook 日まで遡る。
func (cs CalendarSnapshot) PreviousWorkDay(d time.Time, maxLook int) time.Time {
	cur := TruncateDay(d)
	for i := 0; i <= maxLook; i++ {
		if cs.IsWorkDay(cur) {
			return cur
		}
		cur = cur.AddDate(0, 0, -1)
	}
	// 全て休日だった場合は元の日を返す (CRP は当日にクランプ)
	return TruncateDay(d)
}

// LoadSnapshot — DB からカレンダー本体と例外を読み出してスナップショット化
func (s *CalendarService) LoadSnapshot(ctx context.Context, calID uuid.UUID, from, to time.Time) (*CalendarSnapshot, error) {
	cal, err := s.r.Get(ctx, calID)
	if err != nil || cal == nil {
		return nil, err
	}
	exs, err := s.r.ExceptionsInRange(ctx, calID, from, to)
	if err != nil {
		return nil, err
	}
	m := make(map[time.Time]domain.CalendarException, len(exs))
	for _, e := range exs {
		m[TruncateDay(e.ExceptionDate)] = e
	}
	return &CalendarSnapshot{Calendar: *cal, Exceptions: m}, nil
}

// LoadDefaultSnapshot — 既定カレンダーのスナップショットを返す
func (s *CalendarService) LoadDefaultSnapshot(ctx context.Context, from, to time.Time) (*CalendarSnapshot, error) {
	cal, err := s.r.Default(ctx)
	if err != nil || cal == nil {
		return nil, err
	}
	return s.LoadSnapshot(ctx, cal.ID, from, to)
}
