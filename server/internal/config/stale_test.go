package config

import (
	"os"
	"testing"
	"time"
)

func TestEnvStaleAfter(t *testing.T) {
	cases := []struct {
		v    string
		want time.Duration
	}{
		{"", time.Hour},
		{"90m", 90 * time.Minute},
		{"2h", 2 * time.Hour},
		{"2", 2 * time.Hour},
		{"0", time.Hour},
		{"-1", time.Hour},
		{"nope", time.Hour},
	}
	for _, c := range cases {
		t.Run(c.v, func(t *testing.T) {
			os.Unsetenv("AGENTTRACER_STALE_AFTER")
			if c.v != "" {
				t.Setenv("AGENTTRACER_STALE_AFTER", c.v)
			}
			got := envStaleAfter("AGENTTRACER_STALE_AFTER", time.Hour)
			if got != c.want {
				t.Fatalf("v=%q got %v want %v", c.v, got, c.want)
			}
		})
	}
}
