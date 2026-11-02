package filter

import "testing"

func TestSetMatch(t *testing.T) {
	s := Set{}
	s.Add("example.com")
	s.Add("ads.tracker.net")

	tests := []struct {
		name     string
		wantRule string
		wantOK   bool
	}{
		{"example.com", "example.com", true},           // exact
		{"www.example.com", "example.com", true},       // sous-domaine
		{"a.b.c.example.com", "example.com", true},     // sous-domaine profond
		{"ads.tracker.net", "ads.tracker.net", true},   // règle sur un sous-domaine
		{"x.ads.tracker.net", "ads.tracker.net", true}, //
		{"tracker.net", "", false},                     // le parent n'est PAS bloqué
		{"notexample.com", "", false},                  // pas un suffixe de label
		{"example.com.evil.org", "", false},            // le nom doit finir par la règle
		{"com", "", false},
	}
	for _, tt := range tests {
		rule, ok := s.Match(tt.name)
		if rule != tt.wantRule || ok != tt.wantOK {
			t.Errorf("Match(%q) = (%q, %v), attendu (%q, %v)", tt.name, rule, ok, tt.wantRule, tt.wantOK)
		}
	}
}

func BenchmarkSetMatch(b *testing.B) {
	s := Set{}
	for i := 0; i < 300000; i++ {
		s.Add(string(rune('a'+i%26)) + "x" + string(rune('a'+i/26%26)) + ".example" + string(rune('a'+i/676%26)) + ".com")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Match("www.some.deep.sub.domain.example.org")
	}
}
