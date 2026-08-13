package httpapi

import (
	"hash/fnv"
	"net/http"
	"strconv"

	"github.com/kevinnguyen/diaper-raffle/internal/raffle"
	"github.com/kevinnguyen/diaper-raffle/internal/store"
)

// OddsRow is one line of the odds table.
type OddsRow struct {
	GuestID     int64  `json:"guest_id"`
	Name        string `json:"name"`
	DiaperCount int64  `json:"diaper_count"`
	Merged      bool   `json:"merged"`
	// Chance is the probability of finishing in the top prize_count, so it
	// already accounts for nobody winning twice. Guests with no diapers get 0.
	Chance   float64 `json:"chance"`
	Eligible bool    `json:"eligible"`
}

// oddsCacheLimit keeps the cache from growing without bound across many
// events. Rosters change constantly while a host is typing, and each version
// gets its own entry.
const oddsCacheLimit = 64

func (s *Server) handleGetOdds(w http.ResponseWriter, r *http.Request) {
	event, err := s.store.EventBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	guests, err := s.store.ListGuests(r.Context(), event.ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	runs := raffle.DefaultOddsRuns
	if raw := r.URL.Query().Get("runs"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 200000 {
			runs = n
		}
	}

	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"odds": s.odds(event, guests, runs),
		"runs": runs,
	})
}

// odds computes everyone's chance of placing, memoised per roster version.
//
// The seed is derived from the event and roster version rather than the clock,
// so the same roster always reports the same percentages. Reopening the panel
// should not make a number wobble by a point.
func (s *Server) odds(event *store.Event, guests []store.Guest, runs int) []OddsRow {
	key := cacheKey(event, runs)

	s.oddsMu.Lock()
	cached, ok := s.oddsCache[key]
	s.oddsMu.Unlock()
	if ok {
		return cached
	}

	chances := raffle.Odds(store.Candidates(guests), event.PrizeCount, runs, seedFor(key))

	rows := make([]OddsRow, len(guests))
	for i, g := range guests {
		rows[i] = OddsRow{
			GuestID:     g.ID,
			Name:        g.Name,
			DiaperCount: g.DiaperCount,
			Merged:      g.Merged,
			Chance:      chances[g.ID],
			Eligible:    g.DiaperCount > 0,
		}
	}

	s.oddsMu.Lock()
	if len(s.oddsCache) >= oddsCacheLimit {
		// Rosters churn as the host types, so old versions are dead weight.
		// Clearing wholesale is cheaper than tracking recency for a table this
		// small, and the next request just recomputes in a few milliseconds.
		s.oddsCache = make(map[string][]OddsRow, oddsCacheLimit)
	}
	s.oddsCache[key] = rows
	s.oddsMu.Unlock()

	return rows
}

func cacheKey(event *store.Event, runs int) string {
	return strconv.FormatInt(event.ID, 10) + ":" +
		strconv.FormatInt(event.RosterVersion, 10) + ":" +
		strconv.Itoa(event.PrizeCount) + ":" +
		strconv.Itoa(runs)
}

func seedFor(key string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(key))
	return h.Sum64()
}
