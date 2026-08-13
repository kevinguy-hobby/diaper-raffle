package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/kevinnguyen/diaper-raffle/internal/store"
	"github.com/kevinnguyen/diaper-raffle/internal/webui"
)

const sampleRoster = "Jordan Alvarez, 172\nMaya Chen, 0\nSam Patel, 198\nRiley Kim, 26\nDana Okafor, 64\n"

type testAPI struct {
	t      *testing.T
	server *httptest.Server
}

func newTestAPI(t *testing.T) *testAPI {
	t.Helper()

	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	assets, index, err := webui.Assets("")
	if err != nil {
		t.Fatalf("load assets: %v", err)
	}

	// Handler logs are noise unless something breaks, and a failing test
	// prints its own diagnosis.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(New(st, log, assets, index).Handler())
	t.Cleanup(srv.Close)

	return &testAPI{t: t, server: srv}
}

// do makes a request and returns the status and the decoded body.
func (a *testAPI) do(method, path string, body any) (int, map[string]any) {
	a.t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			a.t.Fatalf("encode request: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, a.server.URL+path, reader)
	if err != nil {
		a.t.Fatalf("build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := a.server.Client().Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		a.t.Fatalf("read response: %v", err)
	}
	if len(raw) == 0 {
		return res.StatusCode, nil
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		a.t.Fatalf("%s %s returned %d with unreadable body %q", method, path, res.StatusCode, raw)
	}
	return res.StatusCode, decoded
}

// raw makes a request and returns the status with the untouched body, for the
// cases where what matters is the exact bytes on the wire.
func (a *testAPI) raw(method, path, body string) (int, string) {
	a.t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, a.server.URL+path, reader)
	if err != nil {
		a.t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := a.server.Client().Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		a.t.Fatalf("read response: %v", err)
	}
	return res.StatusCode, string(raw)
}

// event creates a raffle with a roster and returns its slug.
func (a *testAPI) event(roster string) string {
	a.t.Helper()

	status, body := a.do(http.MethodPost, "/api/events", map[string]any{"name": "Baby Shower"})
	if status != http.StatusCreated {
		a.t.Fatalf("create event: got %d, want 201", status)
	}
	slug := body["event"].(map[string]any)["slug"].(string)

	if roster != "" {
		status, _ = a.do(http.MethodPut, "/api/events/"+slug+"/roster", map[string]any{"text": roster})
		if status != http.StatusOK {
			a.t.Fatalf("set roster: got %d, want 200", status)
		}
	}
	return slug
}

func TestHealth(t *testing.T) {
	api := newTestAPI(t)

	status, body := api.do(http.MethodGet, "/api/health", nil)
	if status != http.StatusOK {
		t.Fatalf("got %d, want 200", status)
	}
	if body["status"] != "ok" {
		t.Errorf("status is %v, want ok", body["status"])
	}
}

func TestCreateAndFetchAnEvent(t *testing.T) {
	api := newTestAPI(t)

	status, body := api.do(http.MethodPost, "/api/events",
		map[string]any{"name": "Kevin's Shower", "prize_count": 4})
	if status != http.StatusCreated {
		t.Fatalf("create: got %d, want 201", status)
	}

	event := body["event"].(map[string]any)
	if event["name"] != "Kevin's Shower" {
		t.Errorf("name is %v", event["name"])
	}
	if event["prize_count"].(float64) != 4 {
		t.Errorf("prize count is %v, want 4", event["prize_count"])
	}

	slug := event["slug"].(string)
	status, body = api.do(http.MethodGet, "/api/events/"+slug, nil)
	if status != http.StatusOK {
		t.Fatalf("fetch: got %d, want 200", status)
	}
	if body["draw"] != nil {
		t.Errorf("a new event should have no draw, got %v", body["draw"])
	}
	if len(body["guests"].([]any)) != 0 {
		t.Errorf("a new event should have no guests")
	}
}

func TestUnknownEventIsA404(t *testing.T) {
	api := newTestAPI(t)

	status, body := api.do(http.MethodGet, "/api/events/nope", nil)
	if status != http.StatusNotFound {
		t.Fatalf("got %d, want 404", status)
	}
	if body["error"].(map[string]any)["code"] != "not_found" {
		t.Errorf("error code is %v", body["error"])
	}
}

func TestUnknownEndpointIsAJSON404(t *testing.T) {
	api := newTestAPI(t)

	status, body := api.do(http.MethodGet, "/api/nothing-here", nil)
	if status != http.StatusNotFound {
		t.Fatalf("got %d, want 404", status)
	}
	if body["error"] == nil {
		t.Errorf("want a JSON error body, got %v", body)
	}
}

func TestRosterSaveReturnsTheWholePageState(t *testing.T) {
	api := newTestAPI(t)
	slug := api.event("")

	status, body := api.do(http.MethodPut, "/api/events/"+slug+"/roster",
		map[string]any{"text": sampleRoster})
	if status != http.StatusOK {
		t.Fatalf("got %d, want 200", status)
	}

	guests := body["guests"].([]any)
	if len(guests) != 5 {
		t.Fatalf("got %d guests, want 5", len(guests))
	}

	tally := body["tally"].(map[string]any)
	if tally["entrants"].(float64) != 4 {
		t.Errorf("entrants is %v, want 4 — Maya Chen has none", tally["entrants"])
	}
	if tally["diaper_total"].(float64) != 460 {
		t.Errorf("diaper total is %v, want 460", tally["diaper_total"])
	}
	if tally["sitting_out"].(float64) != 1 {
		t.Errorf("sitting out is %v, want 1", tally["sitting_out"])
	}
	if tally["short"].(bool) {
		t.Errorf("four entrants and three prizes should not be short")
	}
}

func TestTallyFlagsAThinRoster(t *testing.T) {
	api := newTestAPI(t)
	slug := api.event("Only Guest, 10\n")

	_, body := api.do(http.MethodGet, "/api/events/"+slug, nil)
	tally := body["tally"].(map[string]any)

	if !tally["short"].(bool) {
		t.Errorf("one entrant and three prizes should be flagged short")
	}
}

// The whole point of drawing on the server: a stub nobody has torn open must
// not carry a name in any response.
func TestUntornStubsCarryNoName(t *testing.T) {
	api := newTestAPI(t)
	slug := api.event(sampleRoster)

	status, body := api.do(http.MethodPost, "/api/events/"+slug+"/draws", nil)
	if status != http.StatusCreated {
		t.Fatalf("draw: got %d, want 201", status)
	}

	draw := body["draw"].(map[string]any)
	drawID := int(draw["id"].(float64))
	winners := draw["winners"].([]any)
	if len(winners) != 3 {
		t.Fatalf("got %d winners, want 3", len(winners))
	}

	for i, entry := range winners {
		w := entry.(map[string]any)
		if w["revealed"].(bool) {
			t.Errorf("prize %d came back revealed", i+1)
		}
		if w["name"] != nil || w["diaper_count"] != nil || w["guest_id"] != nil {
			t.Errorf("prize %d leaked: %v", i+1, w)
		}
	}

	// And nothing leaks through the raw bytes either — no name should be
	// findable anywhere in the payload.
	_, payload := api.raw(http.MethodGet, "/api/draws/"+strconv.Itoa(drawID), "")
	for _, name := range []string{"Jordan", "Sam Patel", "Riley", "Dana", "Maya"} {
		if strings.Contains(payload, name) {
			t.Errorf("a face-down draw response contains %q: %s", name, payload)
		}
	}
}

func TestRevealingAStubReturnsTheWinner(t *testing.T) {
	api := newTestAPI(t)
	slug := api.event(sampleRoster)

	_, body := api.do(http.MethodPost, "/api/events/"+slug+"/draws", nil)
	drawID := int(body["draw"].(map[string]any)["id"].(float64))

	status, body := api.do(http.MethodPost, "/api/draws/"+strconv.Itoa(drawID)+"/winners/0/reveal", nil)
	if status != http.StatusOK {
		t.Fatalf("reveal: got %d, want 200", status)
	}

	winner := body["winner"].(map[string]any)
	if !winner["revealed"].(bool) {
		t.Errorf("winner is not marked revealed")
	}
	name, _ := winner["name"].(string)
	if name == "" {
		t.Fatalf("reveal returned no name: %v", winner)
	}

	// Fetching the draw again shows prize 1 open and prizes 2 and 3 sealed.
	_, body = api.do(http.MethodGet, "/api/draws/"+strconv.Itoa(drawID), nil)
	winners := body["draw"].(map[string]any)["winners"].([]any)

	if winners[0].(map[string]any)["name"] != name {
		t.Errorf("prize 1 did not stay revealed")
	}
	for i := 1; i < len(winners); i++ {
		if winners[i].(map[string]any)["name"] != nil {
			t.Errorf("prize %d leaked after prize 1 was revealed", i+1)
		}
	}
}

func TestRevealingTwiceIsFine(t *testing.T) {
	api := newTestAPI(t)
	slug := api.event(sampleRoster)

	_, body := api.do(http.MethodPost, "/api/events/"+slug+"/draws", nil)
	drawID := int(body["draw"].(map[string]any)["id"].(float64))
	path := "/api/draws/" + strconv.Itoa(drawID) + "/winners/1/reveal"

	_, first := api.do(http.MethodPost, path, nil)
	status, second := api.do(http.MethodPost, path, nil)

	if status != http.StatusOK {
		t.Fatalf("second reveal: got %d, want 200", status)
	}
	firstName := first["winner"].(map[string]any)["name"]
	if second["winner"].(map[string]any)["name"] != firstName {
		t.Errorf("two taps on the same stub disagreed")
	}
}

func TestRevealingAStubThatIsNotThere(t *testing.T) {
	api := newTestAPI(t)
	slug := api.event(sampleRoster)

	_, body := api.do(http.MethodPost, "/api/events/"+slug+"/draws", nil)
	drawID := int(body["draw"].(map[string]any)["id"].(float64))

	if status, _ := api.do(http.MethodPost, "/api/draws/"+strconv.Itoa(drawID)+"/winners/9/reveal", nil); status != http.StatusNotFound {
		t.Errorf("prize 10 of 3: got %d, want 404", status)
	}
	if status, _ := api.do(http.MethodPost, "/api/draws/abc/winners/0/reveal", nil); status != http.StatusBadRequest {
		t.Errorf("non-numeric draw id: got %d, want 400", status)
	}
}

func TestDrawingWithNobodyEligibleIsRejected(t *testing.T) {
	api := newTestAPI(t)
	slug := api.event("Alpha, 0\nBeta\n")

	status, body := api.do(http.MethodPost, "/api/events/"+slug+"/draws", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", status)
	}
	if body["error"].(map[string]any)["code"] != "invalid" {
		t.Errorf("error code is %v", body["error"])
	}
}

// Editing the roster after a draw should mark the result on screen as out of
// date rather than silently deleting it.
func TestEditingTheRosterMarksTheDrawStale(t *testing.T) {
	api := newTestAPI(t)
	slug := api.event(sampleRoster)

	api.do(http.MethodPost, "/api/events/"+slug+"/draws", nil)

	_, body := api.do(http.MethodGet, "/api/events/"+slug, nil)
	if body["stale"].(bool) {
		t.Errorf("a fresh draw should not be stale")
	}

	_, body = api.do(http.MethodPut, "/api/events/"+slug+"/roster",
		map[string]any{"text": sampleRoster + "Newcomer, 40\n"})
	if !body["stale"].(bool) {
		t.Errorf("the draw should be stale after a roster edit")
	}
	if body["draw"] == nil {
		t.Errorf("the draw should still be on screen, just marked stale")
	}

	// Drawing again clears it.
	api.do(http.MethodPost, "/api/events/"+slug+"/draws", nil)
	_, body = api.do(http.MethodGet, "/api/events/"+slug, nil)
	if body["stale"].(bool) {
		t.Errorf("drawing again should clear the stale flag")
	}
}

func TestDrawHistoryIsNewestFirst(t *testing.T) {
	api := newTestAPI(t)
	slug := api.event(sampleRoster)

	_, first := api.do(http.MethodPost, "/api/events/"+slug+"/draws", nil)
	_, second := api.do(http.MethodPost, "/api/events/"+slug+"/draws", nil)

	status, body := api.do(http.MethodGet, "/api/events/"+slug+"/draws", nil)
	if status != http.StatusOK {
		t.Fatalf("got %d, want 200", status)
	}

	draws := body["draws"].([]any)
	if len(draws) != 2 {
		t.Fatalf("got %d draws, want 2", len(draws))
	}
	if draws[0].(map[string]any)["id"] != second["draw"].(map[string]any)["id"] {
		t.Errorf("history is not newest first")
	}
	if draws[1].(map[string]any)["id"] != first["draw"].(map[string]any)["id"] {
		t.Errorf("the older draw is missing from the history")
	}
}

func TestOddsAddUpToThePrizeCount(t *testing.T) {
	api := newTestAPI(t)
	slug := api.event(sampleRoster)

	status, body := api.do(http.MethodGet, "/api/events/"+slug+"/odds", nil)
	if status != http.StatusOK {
		t.Fatalf("got %d, want 200", status)
	}

	rows := body["odds"].([]any)
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5 — everyone should be listed", len(rows))
	}

	var sum float64
	for _, entry := range rows {
		row := entry.(map[string]any)
		chance := row["chance"].(float64)
		sum += chance

		if row["name"] == "Maya Chen" {
			if row["eligible"].(bool) {
				t.Errorf("Maya Chen has no diapers and should not be eligible")
			}
			if chance != 0 {
				t.Errorf("Maya Chen's chance is %v, want 0", chance)
			}
		}
	}
	// Three prizes among four eligible guests.
	if sum < 2.99 || sum > 3.01 {
		t.Errorf("chances sum to %v, want 3", sum)
	}
}

// The odds are simulated, so they would wobble run to run if the seed moved.
// Pinning it to the roster version keeps the table still.
func TestOddsAreStableForAGivenRoster(t *testing.T) {
	api := newTestAPI(t)
	slug := api.event(sampleRoster)

	_, first := api.raw(http.MethodGet, "/api/events/"+slug+"/odds", "")
	_, second := api.raw(http.MethodGet, "/api/events/"+slug+"/odds", "")

	if first != second {
		t.Errorf("the odds moved between two identical requests:\n%s\n%s", first, second)
	}
}

func TestChangingThePrizeCount(t *testing.T) {
	api := newTestAPI(t)
	slug := api.event(sampleRoster)

	status, body := api.do(http.MethodPatch, "/api/events/"+slug, map[string]any{"prize_count": 2})
	if status != http.StatusOK {
		t.Fatalf("got %d, want 200", status)
	}
	if body["event"].(map[string]any)["prize_count"].(float64) != 2 {
		t.Errorf("prize count is %v, want 2", body["event"].(map[string]any)["prize_count"])
	}

	_, body = api.do(http.MethodPost, "/api/events/"+slug+"/draws", nil)
	if got := len(body["draw"].(map[string]any)["winners"].([]any)); got != 2 {
		t.Errorf("got %d winners after asking for 2 prizes", got)
	}

	if status, _ := api.do(http.MethodPatch, "/api/events/"+slug, map[string]any{"prize_count": 0}); status != http.StatusBadRequest {
		t.Errorf("zero prizes: got %d, want 400", status)
	}
}

func TestGuestEndpoints(t *testing.T) {
	api := newTestAPI(t)
	slug := api.event("Alpha, 10\n")

	// Somebody walks in with a pack.
	status, body := api.do(http.MethodPost, "/api/events/"+slug+"/guests",
		map[string]any{"name": "Beta", "diaper_count": 30})
	if status != http.StatusOK {
		t.Fatalf("add guest: got %d, want 200", status)
	}
	if len(body["guests"].([]any)) != 2 {
		t.Fatalf("got %d guests, want 2", len(body["guests"].([]any)))
	}
	// The textarea has to agree with what just happened.
	if got := body["event"].(map[string]any)["roster_text"]; got != "Alpha, 10\nBeta, 30\n" {
		t.Errorf("roster text is %q", got)
	}

	beta := body["guests"].([]any)[1].(map[string]any)
	betaID := strconv.Itoa(int(beta["id"].(float64)))

	status, body = api.do(http.MethodPatch, "/api/events/"+slug+"/guests/"+betaID,
		map[string]any{"diaper_count": 45})
	if status != http.StatusOK {
		t.Fatalf("update guest: got %d, want 200", status)
	}
	if got := body["tally"].(map[string]any)["diaper_total"].(float64); got != 55 {
		t.Errorf("diaper total is %v, want 55", got)
	}

	status, body = api.do(http.MethodDelete, "/api/events/"+slug+"/guests/"+betaID, nil)
	if status != http.StatusOK {
		t.Fatalf("delete guest: got %d, want 200", status)
	}
	if len(body["guests"].([]any)) != 1 {
		t.Errorf("got %d guests after the delete, want 1", len(body["guests"].([]any)))
	}

	if status, _ := api.do(http.MethodDelete, "/api/events/"+slug+"/guests/9999", nil); status != http.StatusNotFound {
		t.Errorf("deleting a guest who is not there: got %d, want 404", status)
	}
}

func TestDeletingAnEvent(t *testing.T) {
	api := newTestAPI(t)
	slug := api.event(sampleRoster)

	if status, _ := api.do(http.MethodDelete, "/api/events/"+slug, nil); status != http.StatusNoContent {
		t.Fatalf("delete: got %d, want 204", status)
	}
	if status, _ := api.do(http.MethodGet, "/api/events/"+slug, nil); status != http.StatusNotFound {
		t.Errorf("after delete: got %d, want 404", status)
	}
}

func TestListEvents(t *testing.T) {
	api := newTestAPI(t)
	api.event("")
	api.event("")

	status, body := api.do(http.MethodGet, "/api/events", nil)
	if status != http.StatusOK {
		t.Fatalf("got %d, want 200", status)
	}
	if len(body["events"].([]any)) != 2 {
		t.Errorf("got %d events, want 2", len(body["events"].([]any)))
	}
}

func TestMalformedRequestsAreRejectedClearly(t *testing.T) {
	api := newTestAPI(t)
	slug := api.event("")

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"not JSON at all", http.MethodPost, "/api/events", "{oh no"},
		{"empty body", http.MethodPut, "/api/events/" + slug + "/roster", ""},
		{"wrong type", http.MethodPut, "/api/events/" + slug + "/roster", `{"text": 12}`},
		{"unknown field", http.MethodPost, "/api/events", `{"name":"x","colour":"red"}`},
		{"two objects", http.MethodPost, "/api/events", `{"name":"x"}{"name":"y"}`},
		{"no name", http.MethodPost, "/api/events", `{"name":"   "}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, payload := api.raw(tc.method, tc.path, tc.body)
			if status != http.StatusBadRequest {
				t.Errorf("got %d, want 400 (body: %s)", status, payload)
			}
			if !strings.Contains(payload, `"error"`) {
				t.Errorf("want a JSON error body, got %s", payload)
			}
		})
	}
}

func TestOversizedBodiesAreRefused(t *testing.T) {
	api := newTestAPI(t)
	slug := api.event("")

	huge := `{"text":"` + strings.Repeat("a", 2<<20) + `"}`
	status, _ := api.raw(http.MethodPut, "/api/events/"+slug+"/roster", huge)
	if status != http.StatusBadRequest {
		t.Errorf("got %d, want 400", status)
	}
}

// A page route has to survive a hard refresh, so /e/{slug} serves the app
// rather than a 404.
func TestPageRoutesServeTheApp(t *testing.T) {
	api := newTestAPI(t)

	for _, path := range []string{"/", "/e/anything"} {
		res, err := api.server.Client().Get(api.server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()

		if res.StatusCode != http.StatusOK {
			t.Errorf("GET %s: got %d, want 200", path, res.StatusCode)
		}
		if !strings.Contains(string(body), "<title>Diaper Raffle</title>") {
			t.Errorf("GET %s did not serve the app shell", path)
		}
	}
}

func TestStaticAssetsAreServed(t *testing.T) {
	api := newTestAPI(t)

	for _, asset := range []string{"/app.js", "/styles.css"} {
		res, err := api.server.Client().Get(api.server.URL + asset)
		if err != nil {
			t.Fatalf("GET %s: %v", asset, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Errorf("GET %s: got %d, want 200", asset, res.StatusCode)
		}
	}
}
