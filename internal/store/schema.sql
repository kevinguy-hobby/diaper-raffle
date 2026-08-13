-- One shower, one raffle. The slug is what appears in the URL.
CREATE TABLE events (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    slug         TEXT    NOT NULL UNIQUE,
    name         TEXT    NOT NULL,
    prize_count  INTEGER NOT NULL DEFAULT 3,

    -- The roster exactly as it was typed. Guests are the parsed index of this
    -- text; this column is what the textarea round-trips against, so a guest
    -- list keeps whatever order and formatting the host gave it.
    roster_text  TEXT    NOT NULL DEFAULT '',

    -- Bumped on every roster change. A draw records the version it ran
    -- against, which is how the page knows a result is out of date.
    roster_version INTEGER NOT NULL DEFAULT 0,

    created_at   TEXT    NOT NULL,
    updated_at   TEXT    NOT NULL
);

-- The parsed roster: one row per person, counts already merged.
CREATE TABLE guests (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id     INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    name         TEXT    NOT NULL,

    -- Lower-cased, whitespace-collapsed name. Unique per event: this is what
    -- makes a repeated roster line merge instead of creating a second person.
    name_key     TEXT    NOT NULL,

    diaper_count INTEGER NOT NULL DEFAULT 0,

    -- Position in the roster text, so the table reads back in the host's order.
    position     INTEGER NOT NULL DEFAULT 0,

    -- True when more than one roster line fed this row.
    merged       INTEGER NOT NULL DEFAULT 0,

    created_at   TEXT    NOT NULL,
    updated_at   TEXT    NOT NULL,

    UNIQUE (event_id, name_key)
);

CREATE INDEX idx_guests_event_position ON guests (event_id, position);

-- One press of the button.
CREATE TABLE draws (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id       INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    prize_count    INTEGER NOT NULL,

    -- Snapshot of the pool at the moment of the draw, so a result stays
    -- explainable after the roster moves on.
    entrant_count  INTEGER NOT NULL,
    diaper_total   INTEGER NOT NULL,
    roster_version INTEGER NOT NULL,

    created_at     TEXT    NOT NULL
);

CREATE INDEX idx_draws_event_created ON draws (event_id, created_at DESC);

-- One ticket stub. Rows exist face-down: the API withholds the name until
-- revealed_at is set, so a winner cannot be read out of the network tab before
-- the stub is torn open.
CREATE TABLE draw_winners (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    draw_id      INTEGER NOT NULL REFERENCES draws(id) ON DELETE CASCADE,
    prize_index  INTEGER NOT NULL,

    -- Kept for reporting, but nulled rather than cascading if the guest is
    -- later removed from the roster: the result itself must survive.
    guest_id     INTEGER REFERENCES guests(id) ON DELETE SET NULL,

    -- Snapshot of who won and with how many diapers, frozen at draw time.
    guest_name   TEXT    NOT NULL,
    diaper_count INTEGER NOT NULL,

    serial       TEXT    NOT NULL,
    revealed_at  TEXT,

    UNIQUE (draw_id, prize_index)
);

CREATE INDEX idx_draw_winners_draw ON draw_winners (draw_id, prize_index);
