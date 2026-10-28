package migrations

import "testing"

func TestCheckDestructiveRejectsDropColumn(t *testing.T) {
	err := CheckDestructive("999_bad.sql", "ALTER TABLE runs DROP COLUMN model;")
	if err == nil {
		t.Fatal("expected error for DROP COLUMN")
	}
}

func TestCheckDestructiveRejectsTypeRewrite(t *testing.T) {
	err := CheckDestructive("999_bad.sql", "ALTER TABLE runs ALTER COLUMN status TYPE integer USING status::integer;")
	if err == nil {
		t.Fatal("expected error for ALTER COLUMN TYPE")
	}
}

func TestCheckDestructiveAllowsWithMarker(t *testing.T) {
	sql := ExpandContractMarker + "\nALTER TABLE runs DROP COLUMN model;"
	if err := CheckDestructive("999_ok.sql", sql); err != nil {
		t.Fatal(err)
	}
}

func TestCheckDestructiveAllowsCurrentMigrations(t *testing.T) {
	files, err := listSQL()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no sql files")
	}
	for _, name := range files {
		body, err := FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := CheckDestructive(name, string(body)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestCheckDestructiveIgnoresCommentMentions(t *testing.T) {
	sql := "-- we used to DROP COLUMN foo\nALTER TABLE runs ADD COLUMN IF NOT EXISTS bar TEXT;"
	if err := CheckDestructive("005_ok.sql", sql); err != nil {
		t.Fatal(err)
	}
}
