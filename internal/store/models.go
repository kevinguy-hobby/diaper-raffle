package store

// Event is one baby shower's raffle.
type Event struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	PrizeCount    int    `json:"prize_count"`
	RosterText    string `json:"roster_text"`
	RosterVersion int64  `json:"roster_version"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// Guest is one person on the roster, with their counts already merged.
type Guest struct {
	ID          int64  `json:"id"`
	EventID     int64  `json:"event_id"`
	Name        string `json:"name"`
	NameKey     string `json:"name_key"`
	DiaperCount int64  `json:"diaper_count"`
	Position    int    `json:"position"`
	Merged      bool   `json:"merged"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// Draw is one press of the button, with the pool it ran against frozen in.
type Draw struct {
	ID            int64    `json:"id"`
	EventID       int64    `json:"event_id"`
	PrizeCount    int      `json:"prize_count"`
	EntrantCount  int      `json:"entrant_count"`
	DiaperTotal   int64    `json:"diaper_total"`
	RosterVersion int64    `json:"roster_version"`
	CreatedAt     string   `json:"created_at"`
	Winners       []Winner `json:"winners"`
}

// Winner is one ticket stub.
//
// Name and DiaperCount are pointers so an unrevealed stub can serialise them
// as null. The API never fills them in before the stub is torn open — that is
// what keeps the reveal honest, rather than hiding the answer in CSS.
type Winner struct {
	ID          int64   `json:"id"`
	DrawID      int64   `json:"draw_id"`
	PrizeIndex  int     `json:"prize_index"`
	Serial      string  `json:"serial"`
	Revealed    bool    `json:"revealed"`
	RevealedAt  *string `json:"revealed_at"`
	GuestID     *int64  `json:"guest_id"`
	Name        *string `json:"name"`
	DiaperCount *int64  `json:"diaper_count"`
}

// hidden strips a winner down to what a face-down stub is allowed to show.
func (w Winner) hidden() Winner {
	if w.Revealed {
		return w
	}
	w.GuestID = nil
	w.Name = nil
	w.DiaperCount = nil
	w.RevealedAt = nil
	return w
}
