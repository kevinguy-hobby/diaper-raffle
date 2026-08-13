package raffle

import (
	"math"
	"math/rand/v2"
	"sort"
)

// Candidate is somebody eligible to win: a name, a weight, and the database id
// we need to record the result against.
type Candidate struct {
	ID    int64
	Name  string
	Count int64
}

// Eligible keeps the candidates who actually have diapers in the bowl.
func Eligible(pool []Candidate) []Candidate {
	out := make([]Candidate, 0, len(pool))
	for _, c := range pool {
		if c.Count > 0 {
			out = append(out, c)
		}
	}
	return out
}

// Draw picks n distinct winners, weighted so that each diaper is one ticket.
//
// This is Efraimidis–Spirakis weighted sampling without replacement: give each
// candidate the key log(U)/weight for a fresh uniform U, then take the largest
// keys. The result is exactly "draw a ticket, remove that person, draw again",
// which is why nobody can take home two prizes.
//
// The log form is used rather than the equivalent U^(1/weight) because with a
// few hundred diapers that power collapses to 1.0 in float64 and large donors
// stop being distinguishable from each other.
func Draw(pool []Candidate, n int, float func() float64) []Candidate {
	eligible := Eligible(pool)
	if n <= 0 || len(eligible) == 0 {
		return nil
	}

	type keyed struct {
		c   Candidate
		key float64
	}
	keys := make([]keyed, len(eligible))
	for i, c := range eligible {
		keys[i] = keyed{c: c, key: math.Log(float()) / float64(c.Count)}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].key > keys[j].key })

	if n > len(keys) {
		n = len(keys)
	}
	winners := make([]Candidate, n)
	for i := 0; i < n; i++ {
		winners[i] = keys[i].c
	}
	return winners
}

// DefaultOddsRuns is how many draws are simulated to estimate everyone's
// chance. At 4,000 runs a reported percentage is within roughly a point of the
// truth, which is well inside what anyone cares about at a baby shower.
const DefaultOddsRuns = 4000

// Odds estimates each candidate's chance of finishing in the top n, by
// simulation. Simulating rather than computing in closed form is what makes
// the number account for the no-repeat-winners rule.
//
// The returned map is keyed by candidate id. Candidates with no diapers are
// omitted: their chance is zero and they never enter the pool.
func Odds(pool []Candidate, n, runs int, seed uint64) map[int64]float64 {
	eligible := Eligible(pool)
	out := make(map[int64]float64, len(eligible))
	if len(eligible) == 0 || n <= 0 || runs <= 0 {
		return out
	}

	hits := make([]int, len(eligible))
	// Reciprocal weights are the only thing the inner loop needs, and dividing
	// once here keeps a division out of the hot path.
	inverse := make([]float64, len(eligible))
	for i, c := range eligible {
		inverse[i] = 1 / float64(c.Count)
	}

	rng := rand.New(rand.NewPCG(seed, 0x9E3779B97F4A7C15))

	take := n
	if take > len(eligible) {
		take = len(eligible)
	}

	// A partial top-k selection beats sorting the whole pool 4,000 times, and
	// with k of 3 it is a couple of comparisons per candidate.
	topIdx := make([]int, take)
	topKey := make([]float64, take)

	for r := 0; r < runs; r++ {
		filled := 0
		for i := range eligible {
			// Float64 can return exactly 0, whose log is -Inf and would park
			// this candidate at the bottom of this one simulated draw. At odds
			// of 1 in 2^53 that is far below the rounding already applied to
			// the reported percentage, so it is not worth a branch here.
			key := math.Log(rng.Float64()) * inverse[i]

			if filled < take {
				// Insert into the sorted-descending prefix.
				pos := filled
				for pos > 0 && topKey[pos-1] < key {
					topKey[pos], topIdx[pos] = topKey[pos-1], topIdx[pos-1]
					pos--
				}
				topKey[pos], topIdx[pos] = key, i
				filled++
				continue
			}
			if key <= topKey[take-1] {
				continue
			}
			pos := take - 1
			for pos > 0 && topKey[pos-1] < key {
				topKey[pos], topIdx[pos] = topKey[pos-1], topIdx[pos-1]
				pos--
			}
			topKey[pos], topIdx[pos] = key, i
		}
		for i := 0; i < filled; i++ {
			hits[topIdx[i]]++
		}
	}

	for i, c := range eligible {
		out[c.ID] = float64(hits[i]) / float64(runs)
	}
	return out
}
