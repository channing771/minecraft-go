package storage

import "testing"

func TestPlayerMigrationRegistryIsContinuous(t *testing.T) {
	for schema := oldestPlayerSchema; schema < currentPlayerSchema; schema++ {
		if _, ok := playerMigrations[schema]; !ok {
			t.Fatalf("missing migration from schema %d", schema)
		}
	}
	for schema := range playerMigrations {
		if schema < oldestPlayerSchema || schema >= currentPlayerSchema {
			t.Fatalf("unexpected migration from schema %d", schema)
		}
	}
}
