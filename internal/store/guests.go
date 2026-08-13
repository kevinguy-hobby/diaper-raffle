package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/kevinnguyen/diaper-raffle/internal/raffle"
)

const guestColumns = `id, event_id, name, name_key, diaper_count, position, merged, created_at, updated_at`

// ListGuests returns an event's roster in the order it was written.
func (s *Store) ListGuests(ctx context.Context, eventID int64) ([]Guest, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+guestColumns+` FROM guests WHERE event_id = ? ORDER BY position, id`, eventID)
	if err != nil {
		return nil, fmt.Errorf("list guests: %w", err)
	}
	defer rows.Close()
	return scanGuests(rows)
}

// ReplaceRoster parses pasted text and makes it the event's roster.
//
// Guests are matched by name key rather than deleted and re-inserted, so ids
// stay stable across the autosave that fires on every keystroke — a draw from
// five seconds ago still points at real people.
func (s *Store) ReplaceRoster(ctx context.Context, slug, text string) (*Event, []Guest, error) {
	entries := raffle.ParseRoster(text)

	var (
		event  *Event
		guests []Guest
	)
	err := s.tx(ctx, func(tx *sql.Tx) error {
		e, err := eventBySlugTx(ctx, tx, slug)
		if err != nil {
			return err
		}

		existing, err := guestsByKeyTx(ctx, tx, e.ID)
		if err != nil {
			return err
		}

		ts := now()
		keep := make(map[string]bool, len(entries))

		for i, entry := range entries {
			keep[entry.NameKey] = true
			if g, ok := existing[entry.NameKey]; ok {
				if _, err := tx.ExecContext(ctx,
					`UPDATE guests SET name = ?, diaper_count = ?, position = ?, merged = ?, updated_at = ?
					 WHERE id = ?`,
					entry.Name, entry.Count, i, boolToInt(entry.Merged), ts, g.ID); err != nil {
					return fmt.Errorf("update guest %q: %w", entry.Name, err)
				}
				continue
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO guests (event_id, name, name_key, diaper_count, position, merged, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				e.ID, entry.Name, entry.NameKey, entry.Count, i, boolToInt(entry.Merged), ts, ts); err != nil {
				return fmt.Errorf("insert guest %q: %w", entry.Name, err)
			}
		}

		for key, g := range existing {
			if keep[key] {
				continue
			}
			// Past draws keep their own snapshot of the name and count, so
			// removing somebody from the roster does not rewrite history.
			if _, err := tx.ExecContext(ctx, `DELETE FROM guests WHERE id = ?`, g.ID); err != nil {
				return fmt.Errorf("remove guest %q: %w", g.Name, err)
			}
		}

		e.RosterText = text
		e.RosterVersion++
		e.UpdatedAt = ts
		if _, err := tx.ExecContext(ctx,
			`UPDATE events SET roster_text = ?, roster_version = ?, updated_at = ? WHERE id = ?`,
			e.RosterText, e.RosterVersion, e.UpdatedAt, e.ID); err != nil {
			return fmt.Errorf("save roster: %w", err)
		}

		guests, err = listGuestsTx(ctx, tx, e.ID)
		if err != nil {
			return err
		}
		event = e
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return event, guests, nil
}

// AddDiapers logs diapers for one guest, creating them if this is the first
// time they have shown up. This is the "somebody just walked in with a pack"
// path; the textarea is the bulk one.
func (s *Store) AddDiapers(ctx context.Context, slug, name string, count int64) (*Event, *Guest, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil, fmt.Errorf("%w: a guest needs a name", ErrInvalid)
	}
	if count < 0 {
		return nil, nil, fmt.Errorf("%w: diaper count cannot be negative", ErrInvalid)
	}

	var (
		event *Event
		guest *Guest
	)
	err := s.tx(ctx, func(tx *sql.Tx) error {
		e, err := eventBySlugTx(ctx, tx, slug)
		if err != nil {
			return err
		}

		key := raffle.NameKey(name)
		ts := now()

		var id int64
		err = tx.QueryRowContext(ctx,
			`SELECT id FROM guests WHERE event_id = ? AND name_key = ?`, e.ID, key).Scan(&id)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			var position int
			if err := tx.QueryRowContext(ctx,
				`SELECT COALESCE(MAX(position) + 1, 0) FROM guests WHERE event_id = ?`, e.ID).
				Scan(&position); err != nil {
				return fmt.Errorf("find roster position: %w", err)
			}
			res, err := tx.ExecContext(ctx,
				`INSERT INTO guests (event_id, name, name_key, diaper_count, position, merged, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, 0, ?, ?)`,
				e.ID, name, key, count, position, ts, ts)
			if err != nil {
				return fmt.Errorf("add guest: %w", err)
			}
			if id, err = res.LastInsertId(); err != nil {
				return fmt.Errorf("read new guest id: %w", err)
			}
		case err != nil:
			return fmt.Errorf("look up guest: %w", err)
		default:
			if _, err := tx.ExecContext(ctx,
				`UPDATE guests SET diaper_count = diaper_count + ?, updated_at = ? WHERE id = ?`,
				count, ts, id); err != nil {
				return fmt.Errorf("log diapers: %w", err)
			}
		}

		if event, err = syncRosterTextTx(ctx, tx, e); err != nil {
			return err
		}
		guest, err = guestByIDTx(ctx, tx, e.ID, id)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return event, guest, nil
}

// UpdateGuest renames a guest or sets their count outright. Nil fields are
// left alone.
func (s *Store) UpdateGuest(ctx context.Context, slug string, guestID int64, name *string, count *int64) (*Event, *Guest, error) {
	var (
		event *Event
		guest *Guest
	)
	err := s.tx(ctx, func(tx *sql.Tx) error {
		e, err := eventBySlugTx(ctx, tx, slug)
		if err != nil {
			return err
		}
		g, err := guestByIDTx(ctx, tx, e.ID, guestID)
		if err != nil {
			return err
		}

		if name != nil {
			trimmed := strings.TrimSpace(*name)
			if trimmed == "" {
				return fmt.Errorf("%w: a guest needs a name", ErrInvalid)
			}
			g.Name = trimmed
			g.NameKey = raffle.NameKey(trimmed)
		}
		if count != nil {
			if *count < 0 {
				return fmt.Errorf("%w: diaper count cannot be negative", ErrInvalid)
			}
			g.DiaperCount = *count
		}
		g.UpdatedAt = now()

		_, err = tx.ExecContext(ctx,
			`UPDATE guests SET name = ?, name_key = ?, diaper_count = ?, updated_at = ? WHERE id = ?`,
			g.Name, g.NameKey, g.DiaperCount, g.UpdatedAt, g.ID)
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: %q is already on this roster", ErrConflict, g.Name)
		}
		if err != nil {
			return fmt.Errorf("update guest: %w", err)
		}

		if event, err = syncRosterTextTx(ctx, tx, e); err != nil {
			return err
		}
		guest = g
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return event, guest, nil
}

// DeleteGuest takes somebody off the roster. Draws they already won keep their
// name.
func (s *Store) DeleteGuest(ctx context.Context, slug string, guestID int64) (*Event, error) {
	var event *Event
	err := s.tx(ctx, func(tx *sql.Tx) error {
		e, err := eventBySlugTx(ctx, tx, slug)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`DELETE FROM guests WHERE id = ? AND event_id = ?`, guestID, e.ID)
		if err != nil {
			return fmt.Errorf("delete guest: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("delete guest: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("%w: no guest %d on this roster", ErrNotFound, guestID)
		}
		event, err = syncRosterTextTx(ctx, tx, e)
		return err
	})
	if err != nil {
		return nil, err
	}
	return event, nil
}

// syncRosterTextTx rewrites the stored roster text from the guest rows, after
// a change that came in through a guest-level endpoint rather than the
// textarea. Regenerating means the two representations cannot drift.
func syncRosterTextTx(ctx context.Context, tx *sql.Tx, e *Event) (*Event, error) {
	guests, err := listGuestsTx(ctx, tx, e.ID)
	if err != nil {
		return nil, err
	}

	entries := make([]raffle.Entry, len(guests))
	for i, g := range guests {
		entries[i] = raffle.Entry{Name: g.Name, NameKey: g.NameKey, Count: g.DiaperCount}
	}

	e.RosterText = raffle.FormatRoster(entries)
	e.RosterVersion++
	e.UpdatedAt = now()

	if _, err := tx.ExecContext(ctx,
		`UPDATE events SET roster_text = ?, roster_version = ?, updated_at = ? WHERE id = ?`,
		e.RosterText, e.RosterVersion, e.UpdatedAt, e.ID); err != nil {
		return nil, fmt.Errorf("save roster: %w", err)
	}
	// Regenerated text has one line per guest, so nothing is a merge any more.
	if _, err := tx.ExecContext(ctx,
		`UPDATE guests SET merged = 0 WHERE event_id = ? AND merged = 1`, e.ID); err != nil {
		return nil, fmt.Errorf("clear merge flags: %w", err)
	}
	return e, nil
}

func guestsByKeyTx(ctx context.Context, tx *sql.Tx, eventID int64) (map[string]Guest, error) {
	guests, err := listGuestsTx(ctx, tx, eventID)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]Guest, len(guests))
	for _, g := range guests {
		byKey[g.NameKey] = g
	}
	return byKey, nil
}

func listGuestsTx(ctx context.Context, tx *sql.Tx, eventID int64) ([]Guest, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT `+guestColumns+` FROM guests WHERE event_id = ? ORDER BY position, id`, eventID)
	if err != nil {
		return nil, fmt.Errorf("list guests: %w", err)
	}
	defer rows.Close()
	return scanGuests(rows)
}

func guestByIDTx(ctx context.Context, tx *sql.Tx, eventID, guestID int64) (*Guest, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT `+guestColumns+` FROM guests WHERE id = ? AND event_id = ?`, guestID, eventID)
	var g Guest
	var merged int
	err := row.Scan(&g.ID, &g.EventID, &g.Name, &g.NameKey, &g.DiaperCount,
		&g.Position, &merged, &g.CreatedAt, &g.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: no guest %d on this roster", ErrNotFound, guestID)
	}
	if err != nil {
		return nil, fmt.Errorf("read guest: %w", err)
	}
	g.Merged = merged != 0
	return &g, nil
}

func scanGuests(rows *sql.Rows) ([]Guest, error) {
	guests := []Guest{}
	for rows.Next() {
		var g Guest
		var merged int
		if err := rows.Scan(&g.ID, &g.EventID, &g.Name, &g.NameKey, &g.DiaperCount,
			&g.Position, &merged, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, fmt.Errorf("read guest: %w", err)
		}
		g.Merged = merged != 0
		guests = append(guests, g)
	}
	return guests, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
