package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// newEvent creates a raffle with a roster already in place.
func newEvent(t *testing.T, st *Store, roster string) *Event {
	t.Helper()
	ctx := context.Background()

	event, err := st.CreateEvent(ctx, "Baby Shower", "", 3)
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	if roster == "" {
		return event
	}
	event, _, err = st.ReplaceRoster(ctx, event.Slug, roster)
	if err != nil {
		t.Fatalf("set roster: %v", err)
	}
	return event
}

const sampleRoster = "Jordan Alvarez, 172\nMaya Chen, 0\nSam Patel, 198\nRiley Kim, 26\nDana Okafor, 64\n"

func TestCreateEventDerivesASlug(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()

	first, err := st.CreateEvent(ctx, "Kevin & Sam's Shower", "", 3)
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	if first.Slug != "kevin-sam-s-shower" {
		t.Errorf("slug is %q, want %q", first.Slug, "kevin-sam-s-shower")
	}

	// A second raffle with the same name must not collide with the first.
	second, err := st.CreateEvent(ctx, "Kevin & Sam's Shower", "", 3)
	if err != nil {
		t.Fatalf("create second event: %v", err)
	}
	if second.Slug != "kevin-sam-s-shower-2" {
		t.Errorf("second slug is %q, want %q", second.Slug, "kevin-sam-s-shower-2")
	}
}

func TestCreateEventRejectsNonsense(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()

	if _, err := st.CreateEvent(ctx, "   ", "", 3); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty name: got %v, want ErrInvalid", err)
	}
	if _, err := st.CreateEvent(ctx, "Fine", "", MaxPrizeCount+1); !errors.Is(err, ErrInvalid) {
		t.Errorf("too many prizes: got %v, want ErrInvalid", err)
	}
}

func TestEventBySlugReportsMissingEvents(t *testing.T) {
	st := newStore(t)
	if _, err := st.EventBySlug(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestReplaceRosterPersistsTheParsedGuests(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	event := newEvent(t, st, sampleRoster)

	guests, err := st.ListGuests(ctx, event.ID)
	if err != nil {
		t.Fatalf("list guests: %v", err)
	}
	if len(guests) != 5 {
		t.Fatalf("got %d guests, want 5", len(guests))
	}

	// Roster order is preserved.
	want := []string{"Jordan Alvarez", "Maya Chen", "Sam Patel", "Riley Kim", "Dana Okafor"}
	for i, name := range want {
		if guests[i].Name != name {
			t.Errorf("guest %d is %q, want %q", i, guests[i].Name, name)
		}
	}
	if guests[0].DiaperCount != 172 {
		t.Errorf("Jordan has %d diapers, want 172", guests[0].DiaperCount)
	}
	if event.RosterText != sampleRoster {
		t.Errorf("roster text was not stored verbatim")
	}
}

// The textarea saves on a debounce while the host types, so ids have to
// survive an edit — otherwise a draw from a moment ago points at nobody.
func TestReplaceRosterKeepsGuestIDsStable(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	event := newEvent(t, st, sampleRoster)

	before, err := st.ListGuests(ctx, event.ID)
	if err != nil {
		t.Fatalf("list guests: %v", err)
	}
	byName := map[string]int64{}
	for _, g := range before {
		byName[g.Name] = g.ID
	}

	_, after, err := st.ReplaceRoster(ctx, event.Slug,
		"Jordan Alvarez, 200\nMaya Chen, 0\nSam Patel, 198\nRiley Kim, 26\nDana Okafor, 64\nNew Person, 12\n")
	if err != nil {
		t.Fatalf("replace roster: %v", err)
	}

	for _, g := range after {
		if id, existed := byName[g.Name]; existed && id != g.ID {
			t.Errorf("%s changed id from %d to %d across an edit", g.Name, id, g.ID)
		}
	}
	if len(after) != 6 {
		t.Errorf("got %d guests after adding one, want 6", len(after))
	}
	if after[0].DiaperCount != 200 {
		t.Errorf("Jordan has %d diapers after the edit, want 200", after[0].DiaperCount)
	}
}

func TestReplaceRosterDropsGuestsWhoLeaveTheList(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	event := newEvent(t, st, sampleRoster)

	_, after, err := st.ReplaceRoster(ctx, event.Slug, "Jordan Alvarez, 172\n")
	if err != nil {
		t.Fatalf("replace roster: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("got %d guests, want 1", len(after))
	}
	if after[0].Name != "Jordan Alvarez" {
		t.Errorf("the wrong guest survived: %q", after[0].Name)
	}
}

func TestRosterVersionMovesWithEveryChange(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	event := newEvent(t, st, sampleRoster)
	first := event.RosterVersion

	updated, _, err := st.ReplaceRoster(ctx, event.Slug, sampleRoster+"Extra Guest, 5\n")
	if err != nil {
		t.Fatalf("replace roster: %v", err)
	}
	if updated.RosterVersion <= first {
		t.Errorf("roster version went from %d to %d, want an increase", first, updated.RosterVersion)
	}
}

func TestAddDiapersCreatesThenAccumulates(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	event := newEvent(t, st, "")

	_, guest, err := st.AddDiapers(ctx, event.Slug, "Late Arrival", 24)
	if err != nil {
		t.Fatalf("add diapers: %v", err)
	}
	if guest.DiaperCount != 24 {
		t.Errorf("count is %d, want 24", guest.DiaperCount)
	}

	// The same person again, spelled differently, is still the same person.
	_, guest, err = st.AddDiapers(ctx, event.Slug, "late  ARRIVAL", 6)
	if err != nil {
		t.Fatalf("add more diapers: %v", err)
	}
	if guest.DiaperCount != 30 {
		t.Errorf("count is %d after a second drop-off, want 30", guest.DiaperCount)
	}

	guests, err := st.ListGuests(ctx, event.ID)
	if err != nil {
		t.Fatalf("list guests: %v", err)
	}
	if len(guests) != 1 {
		t.Errorf("got %d guests, want 1 — the name should have merged", len(guests))
	}
}

func TestAddDiapersRejectsNonsense(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	event := newEvent(t, st, "")

	if _, _, err := st.AddDiapers(ctx, event.Slug, "  ", 5); !errors.Is(err, ErrInvalid) {
		t.Errorf("blank name: got %v, want ErrInvalid", err)
	}
	if _, _, err := st.AddDiapers(ctx, event.Slug, "Someone", -1); !errors.Is(err, ErrInvalid) {
		t.Errorf("negative count: got %v, want ErrInvalid", err)
	}
}

// A guest-level change has to leave the roster text agreeing with the guest
// rows, or the textarea will show something that is no longer true.
func TestGuestChangesKeepTheRosterTextInStep(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	event := newEvent(t, st, "Alpha, 10\nBeta, 20\n")

	updated, _, err := st.AddDiapers(ctx, event.Slug, "Gamma", 5)
	if err != nil {
		t.Fatalf("add diapers: %v", err)
	}
	want := "Alpha, 10\nBeta, 20\nGamma, 5\n"
	if updated.RosterText != want {
		t.Errorf("roster text is %q, want %q", updated.RosterText, want)
	}

	guests, err := st.ListGuests(ctx, event.ID)
	if err != nil {
		t.Fatalf("list guests: %v", err)
	}
	updated, err = st.DeleteGuest(ctx, event.Slug, guests[0].ID)
	if err != nil {
		t.Fatalf("delete guest: %v", err)
	}
	want = "Beta, 20\nGamma, 5\n"
	if updated.RosterText != want {
		t.Errorf("roster text after a delete is %q, want %q", updated.RosterText, want)
	}
}

func TestUpdateGuestRefusesADuplicateName(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	event := newEvent(t, st, "Alpha, 10\nBeta, 20\n")

	guests, err := st.ListGuests(ctx, event.ID)
	if err != nil {
		t.Fatalf("list guests: %v", err)
	}
	clash := "alpha"
	if _, _, err := st.UpdateGuest(ctx, event.Slug, guests[1].ID, &clash, nil); !errors.Is(err, ErrConflict) {
		t.Errorf("got %v, want ErrConflict", err)
	}
}

func TestRunDrawRecordsWinnersFaceDown(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	event := newEvent(t, st, sampleRoster)

	draw, err := st.RunDraw(ctx, event.Slug)
	if err != nil {
		t.Fatalf("run draw: %v", err)
	}

	if len(draw.Winners) != 3 {
		t.Fatalf("got %d winners, want 3", len(draw.Winners))
	}
	// Maya Chen has no diapers, so four people are eligible.
	if draw.EntrantCount != 4 {
		t.Errorf("entrant count is %d, want 4", draw.EntrantCount)
	}
	if draw.DiaperTotal != 460 {
		t.Errorf("diaper total is %d, want 460", draw.DiaperTotal)
	}
	if draw.RosterVersion != event.RosterVersion {
		t.Errorf("draw recorded roster version %d, want %d", draw.RosterVersion, event.RosterVersion)
	}

	seenSerials := map[string]bool{}
	for i, w := range draw.Winners {
		if w.PrizeIndex != i {
			t.Errorf("winner %d has prize index %d", i, w.PrizeIndex)
		}
		if w.Revealed {
			t.Errorf("prize %d came back already revealed", i+1)
		}
		if w.Name != nil || w.DiaperCount != nil || w.GuestID != nil {
			t.Errorf("prize %d leaked its winner before being torn open", i+1)
		}
		if len(w.Serial) == 0 {
			t.Errorf("prize %d has no serial", i+1)
		}
		if seenSerials[w.Serial] {
			t.Errorf("serial %s appears on two stubs in one draw", w.Serial)
		}
		seenSerials[w.Serial] = true
	}
}

func TestRunDrawRefusesWhenNobodyHasDiapers(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	event := newEvent(t, st, "Alpha, 0\nBeta\n")

	if _, err := st.RunDraw(ctx, event.Slug); !errors.Is(err, ErrInvalid) {
		t.Errorf("got %v, want ErrInvalid", err)
	}
}

func TestRunDrawCapsAtTheEligibleCount(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	event := newEvent(t, st, "Alpha, 10\nBeta, 0\n")

	draw, err := st.RunDraw(ctx, event.Slug)
	if err != nil {
		t.Fatalf("run draw: %v", err)
	}
	if len(draw.Winners) != 1 {
		t.Errorf("got %d winners with one eligible guest, want 1", len(draw.Winners))
	}
}

func TestRevealHandsBackTheNameAndSticks(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	event := newEvent(t, st, sampleRoster)

	draw, err := st.RunDraw(ctx, event.Slug)
	if err != nil {
		t.Fatalf("run draw: %v", err)
	}

	winner, err := st.RevealWinner(ctx, draw.ID, 0)
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}
	if !winner.Revealed || winner.Name == nil || *winner.Name == "" {
		t.Fatalf("reveal returned nothing useful: %+v", winner)
	}
	firstName := *winner.Name
	firstRevealedAt := *winner.RevealedAt

	// Two people tapping the same stub should agree, and the timestamp should
	// record the first tear rather than the latest tap.
	again, err := st.RevealWinner(ctx, draw.ID, 0)
	if err != nil {
		t.Fatalf("reveal again: %v", err)
	}
	if *again.Name != firstName {
		t.Errorf("second reveal says %q, first said %q", *again.Name, firstName)
	}
	if *again.RevealedAt != firstRevealedAt {
		t.Errorf("reveal time moved from %s to %s", firstRevealedAt, *again.RevealedAt)
	}

	// Reloading shows prize 1 open and the rest still sealed.
	reloaded, err := st.DrawByID(ctx, draw.ID)
	if err != nil {
		t.Fatalf("reload draw: %v", err)
	}
	if !reloaded.Winners[0].Revealed || reloaded.Winners[0].Name == nil {
		t.Errorf("prize 1 did not stay revealed across a reload")
	}
	if reloaded.Winners[1].Revealed || reloaded.Winners[1].Name != nil {
		t.Errorf("prize 2 leaked after prize 1 was revealed")
	}
}

func TestRevealRejectsAStubThatDoesNotExist(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	event := newEvent(t, st, sampleRoster)

	draw, err := st.RunDraw(ctx, event.Slug)
	if err != nil {
		t.Fatalf("run draw: %v", err)
	}
	if _, err := st.RevealWinner(ctx, draw.ID, 99); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
	if _, err := st.RevealWinner(ctx, 9999, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

// The point of writing a draw down is that it outlives the roster it came
// from. Removing somebody must not rewrite what already happened.
func TestDrawHistorySurvivesTheGuestLeavingTheRoster(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	event := newEvent(t, st, "Solo Winner, 50\n")

	draw, err := st.RunDraw(ctx, event.Slug)
	if err != nil {
		t.Fatalf("run draw: %v", err)
	}
	if _, err := st.RevealWinner(ctx, draw.ID, 0); err != nil {
		t.Fatalf("reveal: %v", err)
	}

	if _, _, err := st.ReplaceRoster(ctx, event.Slug, "Somebody Else, 10\n"); err != nil {
		t.Fatalf("replace roster: %v", err)
	}

	reloaded, err := st.DrawByID(ctx, draw.ID)
	if err != nil {
		t.Fatalf("reload draw: %v", err)
	}
	if reloaded.Winners[0].Name == nil || *reloaded.Winners[0].Name != "Solo Winner" {
		t.Errorf("the recorded winner's name did not survive the roster change")
	}
	if *reloaded.Winners[0].DiaperCount != 50 {
		t.Errorf("the recorded diaper count did not survive the roster change")
	}
	if reloaded.Winners[0].GuestID != nil {
		t.Errorf("guest id should be cleared once the guest is gone, got %d", *reloaded.Winners[0].GuestID)
	}
}

func TestLatestDrawAndHistory(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	event := newEvent(t, st, sampleRoster)

	if latest, err := st.LatestDraw(ctx, event.ID); err != nil || latest != nil {
		t.Fatalf("before any draw: got %v, %v; want nil, nil", latest, err)
	}

	first, err := st.RunDraw(ctx, event.Slug)
	if err != nil {
		t.Fatalf("first draw: %v", err)
	}
	second, err := st.RunDraw(ctx, event.Slug)
	if err != nil {
		t.Fatalf("second draw: %v", err)
	}

	latest, err := st.LatestDraw(ctx, event.ID)
	if err != nil {
		t.Fatalf("latest draw: %v", err)
	}
	if latest.ID != second.ID {
		t.Errorf("latest draw is %d, want %d", latest.ID, second.ID)
	}

	history, err := st.ListDraws(ctx, event.ID, 0)
	if err != nil {
		t.Fatalf("list draws: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("got %d draws in the history, want 2", len(history))
	}
	if history[0].ID != second.ID || history[1].ID != first.ID {
		t.Errorf("history is not newest first: %d, %d", history[0].ID, history[1].ID)
	}
	if len(history[1].Winners) != 3 {
		t.Errorf("the older draw kept %d winners, want 3", len(history[1].Winners))
	}
}

func TestDeleteEventTakesItsDrawsWithIt(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	event := newEvent(t, st, sampleRoster)

	draw, err := st.RunDraw(ctx, event.Slug)
	if err != nil {
		t.Fatalf("run draw: %v", err)
	}
	if err := st.DeleteEvent(ctx, event.Slug); err != nil {
		t.Fatalf("delete event: %v", err)
	}

	if _, err := st.EventBySlug(ctx, event.Slug); !errors.Is(err, ErrNotFound) {
		t.Errorf("event still there: %v", err)
	}
	if _, err := st.DrawByID(ctx, draw.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("draw outlived its event: %v", err)
	}
	if err := st.DeleteEvent(ctx, event.Slug); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting twice: got %v, want ErrNotFound", err)
	}
}

func TestUpdateEventChangesOnlyWhatIsSent(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	event := newEvent(t, st, "")

	five := 5
	updated, err := st.UpdateEvent(ctx, event.Slug, EventUpdate{PrizeCount: &five})
	if err != nil {
		t.Fatalf("update event: %v", err)
	}
	if updated.PrizeCount != 5 {
		t.Errorf("prize count is %d, want 5", updated.PrizeCount)
	}
	if updated.Name != event.Name {
		t.Errorf("name changed to %q without being asked", updated.Name)
	}

	tooMany := MaxPrizeCount + 1
	if _, err := st.UpdateEvent(ctx, event.Slug, EventUpdate{PrizeCount: &tooMany}); !errors.Is(err, ErrInvalid) {
		t.Errorf("got %v, want ErrInvalid", err)
	}
}

// Renaming must not silently move the link out from under anyone who already
// has it. The slug changes only when the caller asks for it.
func TestRenamingLeavesTheLinkAloneUnlessAsked(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	event := newEvent(t, st, "")
	original := event.Slug

	renamed := "Kev and Jen Baby Shower"
	updated, err := st.UpdateEvent(ctx, event.Slug, EventUpdate{Name: &renamed})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if updated.Name != renamed {
		t.Errorf("name is %q, want %q", updated.Name, renamed)
	}
	if updated.Slug != original {
		t.Errorf("the link moved to %q on its own, want it to stay at %q", updated.Slug, original)
	}
}

func TestChangingTheLink(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	event := newEvent(t, st, sampleRoster)

	// The slug is derived from whatever it is given, so the page can hand it a
	// display name and get a usable link back.
	wanted := "Kev and Jen Baby Shower"
	updated, err := st.UpdateEvent(ctx, event.Slug, EventUpdate{Name: &wanted, Slug: &wanted})
	if err != nil {
		t.Fatalf("change link: %v", err)
	}
	if updated.Slug != "kev-and-jen-baby-shower" {
		t.Fatalf("slug is %q, want %q", updated.Slug, "kev-and-jen-baby-shower")
	}

	// The event, and everything hanging off it, is reachable at the new link.
	moved, err := st.EventBySlug(ctx, updated.Slug)
	if err != nil {
		t.Fatalf("fetch at the new link: %v", err)
	}
	guests, err := st.ListGuests(ctx, moved.ID)
	if err != nil {
		t.Fatalf("guests at the new link: %v", err)
	}
	if len(guests) != 5 {
		t.Errorf("got %d guests after the move, want 5", len(guests))
	}

	// And the old link is gone.
	if _, err := st.EventBySlug(ctx, event.Slug); !errors.Is(err, ErrNotFound) {
		t.Errorf("the old link still resolves: %v", err)
	}
}

func TestChangingTheLinkStepsAroundOneAlreadyTaken(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()

	taken, err := st.CreateEvent(ctx, "Spring Shower", "", 3)
	if err != nil {
		t.Fatalf("create first event: %v", err)
	}
	other := newEvent(t, st, "")

	wanted := "Spring Shower"
	updated, err := st.UpdateEvent(ctx, other.Slug, EventUpdate{Slug: &wanted})
	if err != nil {
		t.Fatalf("change link: %v", err)
	}
	if updated.Slug == taken.Slug {
		t.Fatalf("two events landed on the same link %q", updated.Slug)
	}
	if updated.Slug != "spring-shower-2" {
		t.Errorf("slug is %q, want %q", updated.Slug, "spring-shower-2")
	}
}

func TestChangingTheLinkToNothingIsRejected(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	event := newEvent(t, st, "")

	for _, bad := range []string{"", "   ", "!!!", "—"} {
		if _, err := st.UpdateEvent(ctx, event.Slug, EventUpdate{Slug: &bad}); !errors.Is(err, ErrInvalid) {
			t.Errorf("slug %q: got %v, want ErrInvalid", bad, err)
		}
	}
}

// Asking for the slug it already has is a no-op, not a collision with itself.
func TestChangingTheLinkToItself(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	event := newEvent(t, st, "")

	same := event.Slug
	updated, err := st.UpdateEvent(ctx, event.Slug, EventUpdate{Slug: &same})
	if err != nil {
		t.Fatalf("no-op slug change: %v", err)
	}
	if updated.Slug != same {
		t.Errorf("slug became %q, want %q", updated.Slug, same)
	}
}

// Everything above runs in memory. This one proves the file on disk is real:
// close the store, reopen it, and the party is still there.
func TestDataSurvivesARestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "raffle.db")

	first, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	event, err := first.CreateEvent(ctx, "Persisted Shower", "", 3)
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	if _, _, err := first.ReplaceRoster(ctx, event.Slug, sampleRoster); err != nil {
		t.Fatalf("set roster: %v", err)
	}
	draw, err := first.RunDraw(ctx, event.Slug)
	if err != nil {
		t.Fatalf("run draw: %v", err)
	}
	revealed, err := first.RevealWinner(ctx, draw.ID, 0)
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}
	winnerName := *revealed.Name
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()

	reopened, err := second.EventBySlug(ctx, event.Slug)
	if err != nil {
		t.Fatalf("event after restart: %v", err)
	}
	if reopened.RosterText != sampleRoster {
		t.Errorf("roster did not survive the restart")
	}

	guests, err := second.ListGuests(ctx, reopened.ID)
	if err != nil {
		t.Fatalf("guests after restart: %v", err)
	}
	if len(guests) != 5 {
		t.Errorf("got %d guests after the restart, want 5", len(guests))
	}

	latest, err := second.LatestDraw(ctx, reopened.ID)
	if err != nil {
		t.Fatalf("draw after restart: %v", err)
	}
	if latest == nil || latest.ID != draw.ID {
		t.Fatalf("the draw did not survive the restart")
	}
	if !latest.Winners[0].Revealed || *latest.Winners[0].Name != winnerName {
		t.Errorf("the revealed winner did not survive the restart")
	}
	if latest.Winners[1].Revealed || latest.Winners[1].Name != nil {
		t.Errorf("an untorn stub leaked its name after the restart")
	}
}
