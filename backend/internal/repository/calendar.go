package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type CalendarRepo struct{ db *sqlx.DB }

func (r *CalendarRepo) List(ctx context.Context) ([]domain.WorkCalendar, error) {
	var rows []domain.WorkCalendar
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM work_calendars ORDER BY is_default DESC, code`)
	return rows, err
}

func (r *CalendarRepo) Get(ctx context.Context, id uuid.UUID) (*domain.WorkCalendar, error) {
	var c domain.WorkCalendar
	err := r.db.GetContext(ctx, &c, `SELECT * FROM work_calendars WHERE id=$1`, id)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *CalendarRepo) Default(ctx context.Context) (*domain.WorkCalendar, error) {
	var c domain.WorkCalendar
	err := r.db.GetContext(ctx, &c,
		`SELECT * FROM work_calendars WHERE is_default = true LIMIT 1`)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func (r *CalendarRepo) Create(ctx context.Context, c *domain.WorkCalendar) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO work_calendars
		(id, code, name, is_default,
		 monday_min, tuesday_min, wednesday_min, thursday_min,
		 friday_min, saturday_min, sunday_min, created_at)
		VALUES
		(:id, :code, :name, :is_default,
		 :monday_min, :tuesday_min, :wednesday_min, :thursday_min,
		 :friday_min, :saturday_min, :sunday_min, :created_at)
	`, c)
	return err
}

func (r *CalendarRepo) Update(ctx context.Context, c *domain.WorkCalendar) error {
	_, err := r.db.NamedExecContext(ctx, `
		UPDATE work_calendars SET
		  code=:code, name=:name, is_default=:is_default,
		  monday_min=:monday_min, tuesday_min=:tuesday_min,
		  wednesday_min=:wednesday_min, thursday_min=:thursday_min,
		  friday_min=:friday_min, saturday_min=:saturday_min,
		  sunday_min=:sunday_min
		WHERE id=:id
	`, c)
	return err
}

func (r *CalendarRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM work_calendars WHERE id=$1`, id)
	return err
}

// ----- Exceptions -----

func (r *CalendarRepo) Exceptions(ctx context.Context, calID uuid.UUID) ([]domain.CalendarException, error) {
	var rows []domain.CalendarException
	err := r.db.SelectContext(ctx, &rows,
		`SELECT * FROM calendar_exceptions WHERE calendar_id=$1 ORDER BY exception_date`, calID)
	return rows, err
}

func (r *CalendarRepo) ExceptionsInRange(ctx context.Context, calID uuid.UUID, from, to time.Time) ([]domain.CalendarException, error) {
	var rows []domain.CalendarException
	err := r.db.SelectContext(ctx, &rows, `
		SELECT * FROM calendar_exceptions
		 WHERE calendar_id=$1 AND exception_date BETWEEN $2 AND $3
		 ORDER BY exception_date`, calID, from, to)
	return rows, err
}

func (r *CalendarRepo) AddException(ctx context.Context, e *domain.CalendarException) error {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO calendar_exceptions (id, calendar_id, exception_date, kind, minutes, description)
		VALUES (:id, :calendar_id, :exception_date, :kind, :minutes, :description)
		ON CONFLICT (calendar_id, exception_date) DO UPDATE SET
		  kind=EXCLUDED.kind, minutes=EXCLUDED.minutes, description=EXCLUDED.description
	`, e)
	return err
}

func (r *CalendarRepo) DeleteException(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM calendar_exceptions WHERE id=$1`, id)
	return err
}
