package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MaxPrizeCount bounds how many stubs a draw can produce. Three is the
// default; the ceiling only exists to keep a typo from generating a thousand.
const MaxPrizeCount = 20

const eventColumns = `id, slug, name, prize_count, roster_text, roster_version, created_at, updated_at`

// CreateEvent adds a raffle. If slug is empty one is derived from the name,
// with a numeric suffix if that slug is taken.
func (s *Store) CreateEvent(ctx context.Context, name, slug string, prizeCount int) (*Event, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: an event needs a name", ErrInvalid)
	}
	if prizeCount <= 0 {
		prizeCount = 3
	}
	if prizeCount > MaxPrizeCount {
		return nil, fmt.Errorf("%w: at most %d prizes", ErrInvalid, MaxPrizeCount)
	}

	slug = Slugify(slug)
	if slug == "" {
		slug = Slugify(name)
	}
	if slug == "" {
		slug = "raffle"
	}

	var created *Event
	err := s.tx(ctx, func(tx *sql.Tx) error {
		free, err := s.uniqueSlug(ctx, tx, slug)
		if err != nil {
			return err
		}
		ts := now()
		res, err := tx.ExecContext(ctx,
			`INSERT INTO events (slug, name, prize_count, roster_text, roster_version, created_at, updated_at)
			 VALUES (?, ?, ?, '', 0, ?, ?)`,
			free, name, prizeCount, ts, ts)
		if err != nil {
			return fmt.Errorf("insert event: %w", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("read new event id: %w", err)
		}
		created = &Event{
			ID: id, Slug: free, Name: name, PrizeCount: prizeCount,
			CreatedAt: ts, UpdatedAt: ts,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// uniqueSlug returns base, or base-2, base-3, ... if base is already taken.
func (s *Store) uniqueSlug(ctx context.Context, tx *sql.Tx, base string) (string, error) {
	for n := 1; n < 1000; n++ {
		candidate := base
		if n > 1 {
			candidate = base + "-" + strconv.Itoa(n)
		}
		var exists int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM events WHERE slug = ?`, candidate).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("check slug: %w", err)
		}
	}
	return "", fmt.Errorf("%w: could not find a free slug for %q", ErrConflict, base)
}

// EventBySlug loads one raffle.
func (s *Store) EventBySlug(ctx context.Context, slug string) (*Event, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+eventColumns+` FROM events WHERE slug = ?`, slug)
	return scanEvent(row)
}

// ListEvents returns every raffle, newest first.
func (s *Store) ListEvents(ctx context.Context) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+eventColumns+` FROM events ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	events := []Event{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, *e)
	}
	return events, rows.Err()
}

// EventUpdate carries the fields a caller wants to change. A nil field is left
// alone, so sending just one thing changes just that thing.
type EventUpdate struct {
	Name       *string
	Slug       *string
	PrizeCount *int
}

// UpdateEvent changes an event's name, link, or prize count.
//
// The slug only moves when it is asked to move: renaming a raffle leaves any
// link already shared with guests working. The page sends both together when
// somebody renames, which is the one case where changing the link is the
// point.
func (s *Store) UpdateEvent(ctx context.Context, slug string, u EventUpdate) (*Event, error) {
	var updated *Event
	err := s.tx(ctx, func(tx *sql.Tx) error {
		event, err := eventBySlugTx(ctx, tx, slug)
		if err != nil {
			return err
		}

		if u.Name != nil {
			trimmed := strings.TrimSpace(*u.Name)
			if trimmed == "" {
				return fmt.Errorf("%w: an event needs a name", ErrInvalid)
			}
			event.Name = trimmed
		}
		if u.PrizeCount != nil {
			if *u.PrizeCount <= 0 || *u.PrizeCount > MaxPrizeCount {
				return fmt.Errorf("%w: prize count must be between 1 and %d", ErrInvalid, MaxPrizeCount)
			}
			event.PrizeCount = *u.PrizeCount
		}
		if u.Slug != nil {
			wanted := Slugify(*u.Slug)
			if wanted == "" {
				return fmt.Errorf("%w: %q does not leave anything usable in a link", ErrInvalid, *u.Slug)
			}
			if wanted != event.Slug {
				// Suffix around anything already parked on that slug, the same
				// way a new event would.
				free, err := s.uniqueSlug(ctx, tx, wanted)
				if err != nil {
					return err
				}
				event.Slug = free
			}
		}

		event.UpdatedAt = now()
		_, err = tx.ExecContext(ctx,
			`UPDATE events SET name = ?, slug = ?, prize_count = ?, updated_at = ? WHERE id = ?`,
			event.Name, event.Slug, event.PrizeCount, event.UpdatedAt, event.ID)
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: the link %q is already taken", ErrConflict, event.Slug)
		}
		if err != nil {
			return fmt.Errorf("update event: %w", err)
		}
		updated = event
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// DeleteEvent removes a raffle along with its roster and draw history.
func (s *Store) DeleteEvent(ctx context.Context, slug string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE slug = ?`, slug)
	if err != nil {
		return fmt.Errorf("delete event: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete event: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: no event %q", ErrNotFound, slug)
	}
	return nil
}

func eventBySlugTx(ctx context.Context, tx *sql.Tx, slug string) (*Event, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM events WHERE slug = ?`, slug)
	return scanEvent(row)
}

// scanner covers both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanEvent(row scanner) (*Event, error) {
	var e Event
	err := row.Scan(&e.ID, &e.Slug, &e.Name, &e.PrizeCount, &e.RosterText,
		&e.RosterVersion, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read event: %w", err)
	}
	return &e, nil
}

var (
	nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)
	edgeDashes   = regexp.MustCompile(`^-+|-+$`)
)

// Slugify turns a title into something safe to put in a URL.
func Slugify(s string) string {
	s = nonSlugChars.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
	s = edgeDashes.ReplaceAllString(s, "")
	if len(s) > 60 {
		s = edgeDashes.ReplaceAllString(s[:60], "")
	}
	return s
}
