package httpapi

import (
	"net/http"
	"strconv"

	"github.com/kevinnguyen/diaper-raffle/internal/store"
)

// eventView is everything the page needs to render in one round trip: the
// event, the roster, the running totals, and the draw currently on the table.
type eventView struct {
	Event  *store.Event  `json:"event"`
	Guests []store.Guest `json:"guests"`
	Tally  tally         `json:"tally"`
	Draw   *store.Draw   `json:"draw"`
	// Stale is true when the roster changed after the draw on screen was run.
	Stale bool `json:"stale"`
}

// tally is the row of numbers under the roster box.
type tally struct {
	Entrants    int   `json:"entrants"`
	DiaperTotal int64 `json:"diaper_total"`
	SittingOut  int   `json:"sitting_out"`
	MergedNames int   `json:"merged_names"`
	// Short is set when there are fewer entrants than prizes, which is the one
	// case the host needs warning about before pressing the button.
	Short bool `json:"short"`
}

func buildTally(guests []store.Guest, prizeCount int) tally {
	var t tally
	for _, g := range guests {
		if g.DiaperCount > 0 {
			t.Entrants++
			t.DiaperTotal += g.DiaperCount
		} else {
			t.SittingOut++
		}
		if g.Merged {
			t.MergedNames++
		}
	}
	t.Short = t.Entrants > 0 && t.Entrants < prizeCount
	return t
}

// view assembles the full page state for an event.
func (s *Server) view(r *http.Request, event *store.Event, guests []store.Guest) (*eventView, error) {
	ctx := r.Context()

	if guests == nil {
		var err error
		if guests, err = s.store.ListGuests(ctx, event.ID); err != nil {
			return nil, err
		}
	}
	draw, err := s.store.LatestDraw(ctx, event.ID)
	if err != nil {
		return nil, err
	}

	return &eventView{
		Event:  event,
		Guests: guests,
		Tally:  buildTally(guests, event.PrizeCount),
		Draw:   draw,
		Stale:  draw != nil && draw.RosterVersion != event.RosterVersion,
	}, nil
}

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.ListEvents(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) handleCreateEvent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name       string `json:"name"`
		Slug       string `json:"slug"`
		PrizeCount int    `json:"prize_count"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		s.badRequest(w, r, err.Error())
		return
	}

	event, err := s.store.CreateEvent(r.Context(), body.Name, body.Slug, body.PrizeCount)
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	view, err := s.view(r, event, []store.Guest{})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/e/"+event.Slug)
	s.writeJSON(w, r, http.StatusCreated, view)
}

func (s *Server) handleGetEvent(w http.ResponseWriter, r *http.Request) {
	event, err := s.store.EventBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	view, err := s.view(r, event, nil)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, view)
}

func (s *Server) handleUpdateEvent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name       *string `json:"name"`
		Slug       *string `json:"slug"`
		PrizeCount *int    `json:"prize_count"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		s.badRequest(w, r, err.Error())
		return
	}

	event, err := s.store.UpdateEvent(r.Context(), r.PathValue("slug"), store.EventUpdate{
		Name:       body.Name,
		Slug:       body.Slug,
		PrizeCount: body.PrizeCount,
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	// The link may have moved, and the browser needs to follow it.
	w.Header().Set("Location", "/e/"+event.Slug)
	s.respondWithView(w, r, event, nil)
}

func (s *Server) handleDeleteEvent(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteEvent(r.Context(), r.PathValue("slug")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePutRoster(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		s.badRequest(w, r, err.Error())
		return
	}

	event, guests, err := s.store.ReplaceRoster(r.Context(), r.PathValue("slug"), body.Text)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.respondWithView(w, r, event, guests)
}

func (s *Server) handleAddDiapers(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		DiaperCount int64  `json:"diaper_count"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		s.badRequest(w, r, err.Error())
		return
	}

	event, _, err := s.store.AddDiapers(r.Context(), r.PathValue("slug"), body.Name, body.DiaperCount)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.respondWithView(w, r, event, nil)
}

func (s *Server) handleUpdateGuest(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		s.badRequest(w, r, err.Error())
		return
	}

	var body struct {
		Name        *string `json:"name"`
		DiaperCount *int64  `json:"diaper_count"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		s.badRequest(w, r, err.Error())
		return
	}

	event, _, err := s.store.UpdateGuest(r.Context(), r.PathValue("slug"), id, body.Name, body.DiaperCount)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.respondWithView(w, r, event, nil)
}

func (s *Server) handleDeleteGuest(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		s.badRequest(w, r, err.Error())
		return
	}

	event, err := s.store.DeleteGuest(r.Context(), r.PathValue("slug"), id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.respondWithView(w, r, event, nil)
}

func (s *Server) handleRunDraw(w http.ResponseWriter, r *http.Request) {
	draw, err := s.store.RunDraw(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusCreated, map[string]any{"draw": draw})
}

func (s *Server) handleListDraws(w http.ResponseWriter, r *http.Request) {
	event, err := s.store.EventBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}

	draws, err := s.store.ListDraws(r.Context(), event.ID, limit)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{"draws": draws})
}

func (s *Server) handleGetDraw(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		s.badRequest(w, r, err.Error())
		return
	}

	draw, err := s.store.DrawByID(r.Context(), id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{"draw": draw})
}

// handleReveal is the only endpoint that hands back a winner's name. Every
// other response leaves an untorn stub blank.
func (s *Server) handleReveal(w http.ResponseWriter, r *http.Request) {
	drawID, err := pathInt(r, "id")
	if err != nil {
		s.badRequest(w, r, err.Error())
		return
	}
	prize, err := pathInt(r, "prize")
	if err != nil {
		s.badRequest(w, r, err.Error())
		return
	}

	winner, err := s.store.RevealWinner(r.Context(), drawID, int(prize))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{"winner": winner})
}

// respondWithView is the common tail of every mutation: return the whole page
// state so the browser never has to guess what changed.
func (s *Server) respondWithView(w http.ResponseWriter, r *http.Request, event *store.Event, guests []store.Guest) {
	view, err := s.view(r, event, guests)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, view)
}
