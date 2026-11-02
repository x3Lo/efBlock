package filter

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// collect parse un texte et retourne les domaines bloqués et les exceptions, triés.
func collect(t *testing.T, input string) (blocked, allowed []string) {
	t.Helper()
	err := ParseReader(strings.NewReader(input), func(d string, allow bool) {
		if allow {
			allowed = append(allowed, d)
		} else {
			blocked = append(blocked, d)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(blocked)
	sort.Strings(allowed)
	return
}

func TestParseHostsFormat(t *testing.T) {
	input := `# commentaire
127.0.0.1 localhost
127.0.0.1 localhost.localdomain
::1 ip6-localhost
0.0.0.0 0.0.0.0
0.0.0.0 ads.example.com
0.0.0.0 Tracker.Example.NET   # commentaire en fin de ligne
127.0.0.1 a.example.org b.example.org
`
	blocked, allowed := collect(t, input)
	want := []string{"a.example.org", "ads.example.com", "b.example.org", "tracker.example.net"}
	if !reflect.DeepEqual(blocked, want) {
		t.Errorf("bloqués = %v, attendu %v", blocked, want)
	}
	if len(allowed) != 0 {
		t.Errorf("exceptions inattendues: %v", allowed)
	}
}

func TestParseAdblockFormat(t *testing.T) {
	input := `[Adblock Plus 2.0]
! Title: test
||ads.example.com^
||tracker.example.net^$important
@@||good.example.com^
||site.com/banner.js
||site.org^$third-party
example.com##.banner
||*.wild.example.com^
`
	blocked, allowed := collect(t, input)
	wantB := []string{"ads.example.com", "tracker.example.net"}
	if !reflect.DeepEqual(blocked, wantB) {
		t.Errorf("bloqués = %v, attendu %v", blocked, wantB)
	}
	if !reflect.DeepEqual(allowed, []string{"good.example.com"}) {
		t.Errorf("exceptions = %v", allowed)
	}
}

func TestParsePlainDomains(t *testing.T) {
	input := "ads.example.com\n*.wild.example.org\nnotadomain\n1.2.3.4\ncom\nbad_char!.com\n"
	blocked, _ := collect(t, input)
	want := []string{"ads.example.com", "wild.example.org"}
	if !reflect.DeepEqual(blocked, want) {
		t.Errorf("bloqués = %v, attendu %v", blocked, want)
	}
}

func TestNormalizeDomain(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"Example.COM.", "example.com", true},
		{"a-b.example.com", "a-b.example.com", true},
		{"_dmarc.example.com", "_dmarc.example.com", true},
		{"com", "", false},
		{"", "", false},
		{"-.", "", false},
		{"exa mple.com", "", false},
		{"a..b.com", "", false},
		{"192.168.0.1", "", false},
		{strings.Repeat("a", 64) + ".com", "", false},
	}
	for _, tt := range tests {
		got, ok := normalizeDomain(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("normalizeDomain(%q) = (%q, %v), attendu (%q, %v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
