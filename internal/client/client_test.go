package client

import "testing"

func TestApiBase(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.snyk.io":     "https://api.snyk.io/v1",
		"https://api.snyk.io/":    "https://api.snyk.io/v1",
		"https://api.snyk.io/v1":  "https://api.snyk.io/v1",
		"https://api.snyk.io/v1/": "https://api.snyk.io/v1",
		"https://api.eu.snyk.io":  "https://api.eu.snyk.io/v1",
	} {
		if got := apiBase(in); got != want {
			t.Errorf("apiBase(%q)=%q want %q", in, got, want)
		}
	}
}
