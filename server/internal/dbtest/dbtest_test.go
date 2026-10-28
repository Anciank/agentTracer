package dbtest

import "testing"

func TestAllowTestDatabaseName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"agenttracer_test", true},
		{"foo_test", true},
		{"scratch", true},
		{"agenttracer_scratch", true},
		{"myscratch", true},
		{"agenttracer", false},
		{"agenttracer_prod", false},
		{"postgres", false},
		{"test", false}, // does not end in _test
		{"", false},
		{"  ", false},
	}
	for _, tc := range cases {
		if got := AllowTestDatabaseName(tc.name); got != tc.ok {
			t.Errorf("AllowTestDatabaseName(%q)=%v want %v", tc.name, got, tc.ok)
		}
	}
}

func TestDatabaseNameFromDSN(t *testing.T) {
	cases := []struct {
		dsn  string
		want string
	}{
		{"postgres://u:p@localhost:5432/agenttracer_test?sslmode=disable", "agenttracer_test"},
		{"postgresql://u@127.0.0.1/scratch", "scratch"},
		{"postgres://u:p@h/agenttracer", "agenttracer"},
		{"host=localhost user=u password=p dbname=agenttracer_test sslmode=disable", "agenttracer_test"},
	}
	for _, tc := range cases {
		got, err := DatabaseNameFromDSN(tc.dsn)
		if err != nil {
			t.Errorf("DatabaseNameFromDSN(%q) err=%v", tc.dsn, err)
			continue
		}
		if got != tc.want {
			t.Errorf("DatabaseNameFromDSN(%q)=%q want %q", tc.dsn, got, tc.want)
		}
	}
}

func TestValidateTestDatabaseURL_Guard(t *testing.T) {
	prodLike := []string{
		"postgres://u:p@localhost:5432/agenttracer?sslmode=disable",
		"postgres://u:p@localhost/postgres",
		"host=localhost dbname=agenttracer user=u",
	}
	for _, dsn := range prodLike {
		if err := ValidateTestDatabaseURL(dsn); err == nil {
			t.Errorf("expected reject for %q", dsn)
		}
	}
	ok := []string{
		"postgres://u:p@localhost:5432/agenttracer_test?sslmode=disable",
		"postgres://u:p@localhost:5432/scratch?sslmode=disable",
		"postgres://u:p@localhost:5432/foo_scratch?sslmode=disable",
		"host=localhost dbname=agenttracer_test user=u password=p",
	}
	for _, dsn := range ok {
		if err := ValidateTestDatabaseURL(dsn); err != nil {
			t.Errorf("expected accept for %q: %v", dsn, err)
		}
	}
	if err := ValidateTestDatabaseURL(""); err == nil {
		t.Error("empty DSN should error")
	}
}
