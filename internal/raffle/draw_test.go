package raffle

import (
	"math"
	"math/rand/v2"
	"testing"
)

func guests(counts ...int64) []Candidate {
	out := make([]Candidate, len(counts))
	for i, c := range counts {
		out[i] = Candidate{ID: int64(i + 1), Name: string(rune('A' + i)), Count: c}
	}
	return out
}

// seededFloat gives the draw a reproducible source of randomness so these
// tests do not flake.
func seededFloat(seed uint64) func() float64 {
	rng := rand.New(rand.NewPCG(seed, 1))
	return func() float64 {
		for {
			if v := rng.Float64(); v > 0 {
				return v
			}
		}
	}
}

func TestDrawNeverRepeatsAWinner(t *testing.T) {
	p := guests(100, 50, 25, 10, 5)
	next := seededFloat(7)

	for run := 0; run < 2000; run++ {
		winners := Draw(p, 3, next)
		if len(winners) != 3 {
			t.Fatalf("run %d: got %d winners, want 3", run, len(winners))
		}
		seen := map[int64]bool{}
		for _, w := range winners {
			if seen[w.ID] {
				t.Fatalf("run %d: %s won twice in one draw", run, w.Name)
			}
			seen[w.ID] = true
		}
	}
}

func TestDrawSkipsGuestsWithNoDiapers(t *testing.T) {
	p := guests(10, 0, 10, 0, 10)
	next := seededFloat(11)

	for run := 0; run < 1000; run++ {
		for _, w := range Draw(p, 3, next) {
			if w.Count == 0 {
				t.Fatalf("run %d: %s has no diapers but won a prize", run, w.Name)
			}
		}
	}
}

func TestDrawCapsAtTheNumberOfEligibleGuests(t *testing.T) {
	next := seededFloat(3)

	if got := Draw(guests(5, 0, 0), 3, next); len(got) != 1 {
		t.Errorf("one eligible guest, three prizes: got %d winners, want 1", len(got))
	}
	if got := Draw(guests(0, 0), 3, next); len(got) != 0 {
		t.Errorf("nobody eligible: got %d winners, want 0", len(got))
	}
	if got := Draw(guests(5, 5), 0, next); len(got) != 0 {
		t.Errorf("no prizes: got %d winners, want 0", len(got))
	}
	if got := Draw(nil, 3, next); len(got) != 0 {
		t.Errorf("empty pool: got %d winners, want 0", len(got))
	}
}

// A single-prize draw should hand out wins in proportion to diapers. This is
// the property the whole app rests on, so it is worth checking numerically.
func TestSingleDrawIsProportionalToDiapers(t *testing.T) {
	p := guests(500, 300, 150, 50)
	var total int64
	for _, c := range p {
		total += c.Count
	}

	const runs = 60000
	next := seededFloat(20260812)
	wins := map[int64]int{}
	for i := 0; i < runs; i++ {
		wins[Draw(p, 1, next)[0].ID]++
	}

	for _, c := range p {
		want := float64(c.Count) / float64(total)
		got := float64(wins[c.ID]) / runs
		// Three standard errors of a binomial proportion, plus a little slack.
		tolerance := 3*math.Sqrt(want*(1-want)/runs) + 0.002
		if math.Abs(got-want) > tolerance {
			t.Errorf("%s with %d diapers: won %.4f of draws, want %.4f (±%.4f)",
				c.Name, c.Count, got, want, tolerance)
		}
	}
}

// With very lopsided weights the naive U^(1/w) formulation loses all precision
// and the biggest donors become indistinguishable. The log form should keep
// them ordered.
func TestDrawStaysSensitiveWithLargeCounts(t *testing.T) {
	p := guests(100000, 1000)
	const runs = 20000
	next := seededFloat(99)

	var big int
	for i := 0; i < runs; i++ {
		if Draw(p, 1, next)[0].ID == 1 {
			big++
		}
	}

	share := float64(big) / runs
	want := 100000.0 / 101000.0
	if math.Abs(share-want) > 0.01 {
		t.Errorf("heavy favourite won %.4f of draws, want about %.4f", share, want)
	}
}

func TestOddsAccountForOnePrizePerPerson(t *testing.T) {
	p := guests(400, 300, 200, 100)
	const prizes = 3

	got := Odds(p, prizes, 20000, 42)

	// Every simulated draw hands out three prizes to four people, so the
	// chances must add up to three.
	var sum float64
	for _, v := range got {
		sum += v
	}
	if math.Abs(sum-prizes) > 0.001 {
		t.Errorf("chances sum to %.4f, want %d", sum, prizes)
	}

	// More diapers must never mean a worse chance.
	for i := 0; i < len(p)-1; i++ {
		if got[p[i].ID] < got[p[i+1].ID] {
			t.Errorf("%s (%d diapers) has a worse chance than %s (%d diapers)",
				p[i].Name, p[i].Count, p[i+1].Name, p[i+1].Count)
		}
	}

	// With three prizes among four people, everyone is very likely to place.
	for _, c := range p {
		if got[c.ID] <= 0 || got[c.ID] >= 1 {
			t.Errorf("%s has an implausible chance of %.4f", c.Name, got[c.ID])
		}
	}
}

func TestOddsIsCertaintyWhenPrizesMatchGuests(t *testing.T) {
	p := guests(10, 20, 30)
	got := Odds(p, 3, 500, 1)

	for _, c := range p {
		if got[c.ID] != 1 {
			t.Errorf("%s: chance %.4f, want 1 (three prizes, three guests)", c.Name, got[c.ID])
		}
	}
}

func TestOddsMatchesASinglePrizeDrawsWeights(t *testing.T) {
	p := guests(600, 300, 100)
	got := Odds(p, 1, 40000, 5)

	for _, c := range p {
		want := float64(c.Count) / 1000
		if math.Abs(got[c.ID]-want) > 0.01 {
			t.Errorf("%s: chance %.4f, want about %.4f", c.Name, got[c.ID], want)
		}
	}
}

func TestOddsIgnoresGuestsWithNoDiapers(t *testing.T) {
	p := guests(10, 0, 10)
	got := Odds(p, 2, 1000, 1)

	if _, ok := got[p[1].ID]; ok {
		t.Errorf("guest with no diapers should not appear in the odds")
	}
	if len(got) != 2 {
		t.Errorf("got %d entries in the odds, want 2", len(got))
	}
}

func TestOddsHandlesAnEmptyPool(t *testing.T) {
	if got := Odds(nil, 3, 1000, 1); len(got) != 0 {
		t.Errorf("got %d entries for an empty pool, want 0", len(got))
	}
	if got := Odds(guests(0, 0), 3, 1000, 1); len(got) != 0 {
		t.Errorf("got %d entries when nobody is eligible, want 0", len(got))
	}
}

func TestSerialsAreDistinctAndSixDigits(t *testing.T) {
	got := Serials(20)
	seen := map[string]bool{}
	for _, s := range got {
		if len(s) != SerialLength {
			t.Errorf("serial %q has %d digits, want %d", s, len(s), SerialLength)
		}
		for _, r := range s {
			if r < '0' || r > '9' {
				t.Errorf("serial %q contains a non-digit", s)
			}
		}
		if seen[s] {
			t.Errorf("serial %q was handed out twice", s)
		}
		seen[s] = true
	}
}

func TestCryptoFloatStaysInsideTheUnitInterval(t *testing.T) {
	for i := 0; i < 10000; i++ {
		v := CryptoFloat()
		if v <= 0 || v >= 1 {
			t.Fatalf("CryptoFloat returned %v, want strictly between 0 and 1", v)
		}
	}
}
