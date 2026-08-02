package storage

import "testing"

func TestMigrationFilesAreOrdered(t *testing.T) {
	files, err := migrationFiles()
	if err != nil {
		t.Fatalf("migrationFiles() error = %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one migration")
	}
	var previous int64
	for i, file := range files {
		version, err := migrationVersion(file)
		if err != nil {
			t.Fatalf("migrationVersion(%q) error = %v", file, err)
		}
		if i > 0 && version <= previous {
			t.Fatalf("migration versions are not strictly increasing: %v", files)
		}
		previous = version
	}
}
