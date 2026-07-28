package storage

import "testing"

func TestMigrationRegistryIsContinuous(t *testing.T) {
	for schema := oldestChunkSchema; schema < currentChunkSchema; schema++ {
		if _, ok := chunkMigrations[schema]; !ok {
			t.Fatalf("missing migration from schema %d", schema)
		}
	}
	for schema := range chunkMigrations {
		if schema < oldestChunkSchema || schema >= currentChunkSchema {
			t.Fatalf("unexpected migration from schema %d", schema)
		}
	}
}
