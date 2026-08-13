# Diaper Raffle

A weighted raffle for a baby shower. Every diaper somebody brings is one ticket
in the bowl, three prizes come out, and nobody can take home two.

Built from `diaper-raffle-draw.html` — the design mock is unchanged in spirit,
but the roster and every draw now live in a database instead of evaporating
when the tab closes.

```
go run ./cmd/server
# → http://localhost:8080
```

That is the whole setup. The database is a single SQLite file created on first
run, and the web assets are compiled into the binary.

## What it does

Paste a roster — names and diaper counts, straight out of a spreadsheet — and
press the button. Three ticket stubs come out face down. Tap each one to tear
it open.

- **Weighted properly.** 200 diapers is 200 tickets. A single-prize draw is
  exactly proportional; the three-prize draw is the same thing repeated with
  the winner removed.
- **Nobody wins twice.** Enforced by the sampling method, not by retrying.
- **Everything is saved.** Refresh the page, close the laptop, hand it to
  somebody else — the roster, the draw, and which stubs have been opened are
  all still there.
- **The odds are honest.** The table shows each guest's real chance of placing
  in the top three, accounting for the one-prize-per-person rule.

## Three decisions worth knowing about

**The draw runs on the server.** Not because it is faster — because a winner
who has not been revealed yet must not be sitting in the browser where a guest
with the developer console open can read it. An untorn stub comes back from the
API with `"name": null`. The reveal endpoint is the only thing that will tell
you who won, and it writes down that it did.

**The roster is stored twice, on purpose.** `events.roster_text` is the
document — exactly what was typed, in the order it was typed. The `guests`
table is the parsed index of it: merged, counted, queryable. Every write goes
through one of them and regenerates the other, so they cannot drift.

**A roster edit does not delete the draw on screen.** It marks it stale. The
old mock cleared the winners on every keystroke; that is the right call when
nothing is saved and the wrong one when everything is. Each draw records the
`roster_version` it ran against, and the page says so when they no longer match.

## How the winners are picked

Efraimidis–Spirakis weighted sampling without replacement: give each guest the
key `log(U) / diapers` for a fresh uniform `U`, then take the largest keys.

Two details that matter:

- The randomness comes from `crypto/rand`, not `math/rand`. This is a game
  people are watching.
- The key is computed in log form rather than as `U^(1/diapers)`. With a few
  hundred diapers that power collapses to `1.0` in float64 and the biggest
  donors stop being distinguishable from each other — the person who brought
  500 would quietly stop beating the person who brought 200. There is a test
  for this (`TestDrawStaysSensitiveWithLargeCounts`).

The odds table is a simulation — 4,000 draws, counting how often each person
places. Closed-form top-three inclusion probabilities are possible but fiddly,
and simulation makes the no-repeat rule fall out for free. The seed is derived
from the roster version, so the same roster always reports the same
percentages; the numbers do not wobble when you reopen the panel.

## Roster formats it accepts

One guest per line. All of these work:

```
Jordan Alvarez	172        ← tab separated, pasted from a spreadsheet
Riley Kim, 26              ← comma
Dana Okafor; 64            ← semicolon or colon
Big Spender, 1,024         ← thousands separators
Smith, John, 20            ← commas inside the name are fine
No Number Here             ← counts as zero, sits the draw out
```

Repeated names are summed and flagged as merged. Negative counts become zero.
Fractional counts are truncated — a diaper is a whole thing.

## API

Everything the page does, it does through this.

| | |
|---|---|
| `GET /api/health` | liveness, checks the database |
| `GET /api/events` | list raffles |
| `POST /api/events` | create one — `{name, slug?, prize_count?}` |
| `GET /api/events/{slug}` | full page state: event, guests, tally, current draw, stale flag |
| `PATCH /api/events/{slug}` | `{name?, prize_count?}` |
| `DELETE /api/events/{slug}` | remove a raffle and its history |
| `PUT /api/events/{slug}/roster` | `{text}` — parse and replace the roster |
| `GET /api/events/{slug}/odds` | everyone's chance of placing |
| `POST /api/events/{slug}/guests` | `{name, diaper_count}` — log a drop-off, creates or adds |
| `PATCH /api/events/{slug}/guests/{id}` | `{name?, diaper_count?}` |
| `DELETE /api/events/{slug}/guests/{id}` | |
| `POST /api/events/{slug}/draws` | run a draw, returns face-down stubs |
| `GET /api/events/{slug}/draws` | history, newest first |
| `GET /api/draws/{id}` | one draw |
| `POST /api/draws/{id}/winners/{prize}/reveal` | tear a stub open — the only route that returns a name |

Mutations return the whole page state rather than a fragment, so the browser
never has to guess what else changed. Errors are
`{"error": {"code": "...", "message": "..."}}` with `not_found`, `invalid`,
`conflict`, or `internal`.

The guest endpoints exist for the "somebody just walked in with a pack" flow
and are not wired into the page, which drives everything through the textarea.

## Data model

```
events        the raffle. roster_text, roster_version, prize_count
  guests      parsed roster. unique on (event_id, name_key) — this is what
              makes repeated lines merge instead of duplicating a person
  draws       one press of the button. snapshots entrant_count, diaper_total
              and roster_version so a result stays explainable
    draw_winners
              one stub. guest_name and diaper_count are frozen copies, and
              guest_id is nulled rather than cascaded if that guest later
              leaves the roster — the result outlives the roster it came from
```

Migrations run on startup, tracked by SQLite's `user_version`.

## Running it

```
make run          # build and start on :8080
make dev          # assets served from disk, edit CSS without rebuilding
make test         # go test ./...
make check        # fmt, vet, and the full suite with -race
```

Flags: `-addr` (default `:8080`), `-db` (default `diaper-raffle.db`), `-dev`,
`-verbose`. `ADDR` and `DB_PATH` work too.

To run it at the party, `make build` and copy the one binary. It has no runtime
dependencies. Point `-db` somewhere you will not lose.

## Hosting it

One constraint drives every option: **SQLite is a file, so the host needs a
real disk that survives a restart.** Plain Heroku, Vercel, and Cloud Run
without a mounted volume all have ephemeral filesystems — the app will appear
to work and then lose the roster and every draw the first time it redeploys or
scales to zero.

**For the day of the shower — run it on the laptop.** Genuinely the best
option. `make run`, then either point guests at your LAN address
(`http://192.168.x.x:8080`) or open it to the internet for the afternoon:

```
cloudflared tunnel --url http://localhost:8080     # gives you a public https URL
# or, if you already use Tailscale:
tailscale funnel 8080
```

No account, no deploy, no cost, and the database is a file on your own machine
that you can back up by copying it.

**For a URL that stays up — Fly.io.** `Dockerfile` and `fly.toml` are here and
configured for it:

```
fly launch --no-deploy --copy-config --name your-app-name
fly volumes create raffle_data --size 1 --region <your region>
fly deploy
```

The volume is the whole point — it is what makes the SQLite file survive a
deploy. `min_machines_running = 1` is deliberate too: SQLite takes one writer,
and two machines would each end up hosting their own separate party.

**Anywhere else with a persistent disk** works the same way: build the binary,
copy it up, run it behind Caddy or nginx for TLS, point `-db` at a path on a
disk you will not lose. A $4 VPS is plenty — the odds simulation is the
heaviest thing this does and it takes a few milliseconds.

Whatever you pick, `make backup` takes a consistent copy of the database while
the server is running. Do that after the draw.

### One thing to know before you expose it

There is no authentication. Anyone with the link can edit the roster, press the
button, and tear stubs open. For a baby shower on a private URL for an
afternoon that is the right amount of security, and it is why the draw runs
server-side — the honesty guarantee is about what the page can see, not about
who is allowed in. If you need to hand it to a wider audience, put it behind
Cloudflare Access or a basic-auth reverse proxy rather than adding accounts.

## Tests

```
go test ./...
```

The raffle math is tested statistically — proportionality to within three
standard errors, chances summing to the prize count, no duplicate winners
across thousands of draws. The store and API are tested through their real
interfaces, including one test that closes the database, reopens it, and checks
the party is still there.

The one worth reading is `TestUntornStubsCarryNoName`: it greps the raw HTTP
response for every guest's name and fails if it finds one.
