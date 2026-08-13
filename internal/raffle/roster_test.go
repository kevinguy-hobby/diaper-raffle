package raffle

import (
	"reflect"
	"testing"
)

func TestParseRoster(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Entry
	}{
		{
			name: "tab separated, straight from a spreadsheet",
			in:   "Jordan Alvarez\t172\nMaya Chen\t0\nSam Patel\t198",
			want: []Entry{
				{Name: "Jordan Alvarez", NameKey: "jordan alvarez", Count: 172},
				{Name: "Maya Chen", NameKey: "maya chen", Count: 0},
				{Name: "Sam Patel", NameKey: "sam patel", Count: 198},
			},
		},
		{
			name: "comma separated",
			in:   "Riley Kim, 26\nDana Okafor,64",
			want: []Entry{
				{Name: "Riley Kim", NameKey: "riley kim", Count: 26},
				{Name: "Dana Okafor", NameKey: "dana okafor", Count: 64},
			},
		},
		{
			name: "other separators",
			in:   "Ada Lovelace 12\nGrace Hopper; 7\nAlan Turing: 3",
			want: []Entry{
				{Name: "Ada Lovelace", NameKey: "ada lovelace", Count: 12},
				{Name: "Grace Hopper", NameKey: "grace hopper", Count: 7},
				{Name: "Alan Turing", NameKey: "alan turing", Count: 3},
			},
		},
		{
			name: "thousands separators survive",
			in:   "Big Spender, 1,024\nBigger Spender\t12,500",
			want: []Entry{
				{Name: "Big Spender", NameKey: "big spender", Count: 1024},
				{Name: "Bigger Spender", NameKey: "bigger spender", Count: 12500},
			},
		},
		{
			name: "a name with a comma in it keeps its count",
			in:   "Smith, John, 20",
			want: []Entry{
				{Name: "Smith, John", NameKey: "smith, john", Count: 20},
			},
		},
		{
			name: "no number means zero, and zero sits out",
			in:   "Just A Name",
			want: []Entry{
				{Name: "Just A Name", NameKey: "just a name", Count: 0},
			},
		},
		{
			name: "repeated names are summed and flagged",
			in:   "Sam Patel, 10\nOther Guest, 5\nsam patel, 8\nSAM  PATEL, 2",
			want: []Entry{
				{Name: "Sam Patel", NameKey: "sam patel", Count: 20, Merged: true},
				{Name: "Other Guest", NameKey: "other guest", Count: 5},
			},
		},
		{
			name: "negative counts are treated as zero",
			in:   "Oops, -5",
			want: []Entry{
				{Name: "Oops", NameKey: "oops", Count: 0},
			},
		},
		{
			name: "fractional counts truncate",
			in:   "Half Measure, 7.9",
			want: []Entry{
				{Name: "Half Measure", NameKey: "half measure", Count: 7},
			},
		},
		{
			name: "blank lines and CRLF are ignored",
			in:   "Alpha, 1\r\n\r\n   \nBeta, 2\r\n",
			want: []Entry{
				{Name: "Alpha", NameKey: "alpha", Count: 1},
				{Name: "Beta", NameKey: "beta", Count: 2},
			},
		},
		{
			name: "trailing spreadsheet columns are skipped until a number shows up",
			in:   "Padded Row\t\tnotes\t42\t",
			want: []Entry{
				{Name: "Padded Row", NameKey: "padded row", Count: 42},
			},
		},
		{
			name: "empty roster",
			in:   "",
			want: []Entry{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseRoster(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseRoster(%q)\n got %+v\nwant %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestFormatRosterRoundTrips(t *testing.T) {
	original := "Jordan Alvarez, 172\nMaya Chen, 0\nSam Patel, 198\n"
	got := FormatRoster(ParseRoster(original))
	if got != original {
		t.Errorf("round trip changed the roster\n got %q\nwant %q", got, original)
	}
}
