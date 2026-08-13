package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kevinnguyen/diaper-raffle/internal/raffle"
)

const drawColumns = `id, event_id, prize_count, entrant_count, diaper_total, roster_version, created_at`

// RunDraw picks the winners and writes them down in one transaction.
//
// The draw happens here rather than in the browser for two reasons: the result
// has to survive a refresh, and a winner nobody has torn open yet must not be
// sitting in the page's memory where a curious guest can read it.
func (s *Store) RunDraw(ctx context.Context, slug string) (*Draw, error) {
	var draw *Draw
	err := s.tx(ctx, func(tx *sql.Tx) error {
		event, err := eventBySlugTx(ctx, tx, slug)
		if err != nil {
			return err
		}
		guests, err := listGuestsTx(ctx, tx, event.ID)
		if err != nil {
			return err
		}

		pool := Candidates(guests)
		eligible := raffle.Eligible(pool)
		if len(eligible) == 0 {
			return fmt.Errorf("%w: nobody on the roster has any diapers logged", ErrInvalid)
		}

		var diaperTotal int64
		for _, c := range eligible {
			diaperTotal += c.Count
		}

		winners := raffle.Draw(pool, event.PrizeCount, raffle.CryptoFloat)
		serials := raffle.Serials(len(winners))
		ts := now()

		res, err := tx.ExecContext(ctx,
			`INSERT INTO draws (event_id, prize_count, entrant_count, diaper_total, roster_version, created_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			event.ID, event.PrizeCount, len(eligible), diaperTotal, event.RosterVersion, ts)
		if err != nil {
			return fmt.Errorf("record draw: %w", err)
		}
		drawID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("read new draw id: %w", err)
		}

		for i, w := range winners {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO draw_winners (draw_id, prize_index, guest_id, guest_name, diaper_count, serial, revealed_at)
				 VALUES (?, ?, ?, ?, ?, ?, NULL)`,
				drawID, i, w.ID, w.Name, w.Count, serials[i]); err != nil {
				return fmt.Errorf("record winner: %w", err)
			}
		}

		draw, err = drawByIDTx(ctx, tx, drawID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return draw, nil
}

// LatestDraw returns the most recent draw for an event, or nil if the button
// has not been pressed yet.
func (s *Store) LatestDraw(ctx context.Context, eventID int64) (*Draw, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+drawColumns+` FROM draws WHERE event_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`,
		eventID)

	draw, err := scanDraw(row)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if draw.Winners, err = s.winners(ctx, draw.ID); err != nil {
		return nil, err
	}
	return draw, nil
}

// ListDraws returns an event's draw history, newest first.
func (s *Store) ListDraws(ctx context.Context, eventID int64, limit int) ([]Draw, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+drawColumns+` FROM draws WHERE event_id = ?
		 ORDER BY created_at DESC, id DESC LIMIT ?`, eventID, limit)
	if err != nil {
		return nil, fmt.Errorf("list draws: %w", err)
	}
	defer rows.Close()

	draws := []Draw{}
	for rows.Next() {
		d, err := scanDraw(rows)
		if err != nil {
			return nil, err
		}
		draws = append(draws, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list draws: %w", err)
	}

	for i := range draws {
		if draws[i].Winners, err = s.winners(ctx, draws[i].ID); err != nil {
			return nil, err
		}
	}
	return draws, nil
}

// DrawByID loads one draw with its stubs.
func (s *Store) DrawByID(ctx context.Context, drawID int64) (*Draw, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+drawColumns+` FROM draws WHERE id = ?`, drawID)
	draw, err := scanDraw(row)
	if err != nil {
		return nil, err
	}
	if draw.Winners, err = s.winners(ctx, draw.ID); err != nil {
		return nil, err
	}
	return draw, nil
}

// RevealWinner tears open one stub and returns what was printed on it.
//
// Revealing twice is not an error: two people tapping the same stub should
// both see the same name rather than one of them getting a failure.
func (s *Store) RevealWinner(ctx context.Context, drawID int64, prizeIndex int) (*Winner, error) {
	var winner *Winner
	err := s.tx(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx,
			`SELECT id, draw_id, prize_index, guest_id, guest_name, diaper_count, serial, revealed_at
			 FROM draw_winners WHERE draw_id = ? AND prize_index = ?`, drawID, prizeIndex)

		w, err := scanWinner(row)
		if err != nil {
			return err
		}
		if !w.Revealed {
			ts := now()
			if _, err := tx.ExecContext(ctx,
				`UPDATE draw_winners SET revealed_at = ? WHERE id = ? AND revealed_at IS NULL`,
				ts, w.ID); err != nil {
				return fmt.Errorf("reveal winner: %w", err)
			}
			w.Revealed = true
			w.RevealedAt = &ts
		}
		winner = w
		return nil
	})
	if err != nil {
		return nil, err
	}
	return winner, nil
}

// winners loads a draw's stubs face-down: unrevealed rows come back without a
// name attached.
func (s *Store) winners(ctx context.Context, drawID int64) ([]Winner, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, draw_id, prize_index, guest_id, guest_name, diaper_count, serial, revealed_at
		 FROM draw_winners WHERE draw_id = ? ORDER BY prize_index`, drawID)
	if err != nil {
		return nil, fmt.Errorf("list winners: %w", err)
	}
	defer rows.Close()

	winners := []Winner{}
	for rows.Next() {
		w, err := scanWinner(rows)
		if err != nil {
			return nil, err
		}
		winners = append(winners, w.hidden())
	}
	return winners, rows.Err()
}

// Candidates turns roster rows into something the draw can weigh.
func Candidates(guests []Guest) []raffle.Candidate {
	out := make([]raffle.Candidate, len(guests))
	for i, g := range guests {
		out[i] = raffle.Candidate{ID: g.ID, Name: g.Name, Count: g.DiaperCount}
	}
	return out
}

func drawByIDTx(ctx context.Context, tx *sql.Tx, drawID int64) (*Draw, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+drawColumns+` FROM draws WHERE id = ?`, drawID)
	draw, err := scanDraw(row)
	if err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(ctx,
		`SELECT id, draw_id, prize_index, guest_id, guest_name, diaper_count, serial, revealed_at
		 FROM draw_winners WHERE draw_id = ? ORDER BY prize_index`, drawID)
	if err != nil {
		return nil, fmt.Errorf("list winners: %w", err)
	}
	defer rows.Close()

	draw.Winners = []Winner{}
	for rows.Next() {
		w, err := scanWinner(rows)
		if err != nil {
			return nil, err
		}
		draw.Winners = append(draw.Winners, w.hidden())
	}
	return draw, rows.Err()
}

func scanDraw(row scanner) (*Draw, error) {
	var d Draw
	err := row.Scan(&d.ID, &d.EventID, &d.PrizeCount, &d.EntrantCount,
		&d.DiaperTotal, &d.RosterVersion, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read draw: %w", err)
	}
	d.Winners = []Winner{}
	return &d, nil
}

func scanWinner(row scanner) (*Winner, error) {
	var (
		w          Winner
		guestID    sql.NullInt64
		name       string
		count      int64
		revealedAt sql.NullString
	)
	err := row.Scan(&w.ID, &w.DrawID, &w.PrizeIndex, &guestID, &name, &count, &w.Serial, &revealedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: no such stub", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read winner: %w", err)
	}

	w.Name = &name
	w.DiaperCount = &count
	if guestID.Valid {
		w.GuestID = &guestID.Int64
	}
	if revealedAt.Valid {
		w.Revealed = true
		w.RevealedAt = &revealedAt.String
	}
	return &w, nil
}
