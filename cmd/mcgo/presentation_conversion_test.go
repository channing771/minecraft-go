//go:build darwin

package main

import (
	"reflect"
	"slices"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/client"
	"minecraft-go/internal/render"
)

var (
	presentationBenchmarkPresentations []client.RemotePresentation
	presentationBenchmarkAvatars       []render.Avatar
	presentationBenchmarkTags          []render.NameTag
)

// Mutation killed: copying sorted presentations or allocating fresh avatar/tag
// slices makes the warmed conversion path allocate every frame.
func TestRemoteRenderPresentationsSortedIntoReusesEquivalentStorage(t *testing.T) {
	presentations := presentationConversionFixture()
	wantAvatars, wantTags := remoteRenderPresentations(presentations)
	sorted := append([]client.RemotePresentation(nil), presentations...)
	slices.SortFunc(sorted, func(left, right client.RemotePresentation) int {
		return slices.Compare(left.PlayerID[:], right.PlayerID[:])
	})

	avatars := make([]render.Avatar, 0, len(sorted))
	tags := make([]render.NameTag, 0, len(sorted))
	avatars, tags = remoteRenderPresentationsSortedInto(avatars, tags, sorted)
	allocations := testing.AllocsPerRun(1000, func() {
		avatars, tags = remoteRenderPresentationsSortedInto(avatars[:0], tags[:0], sorted)
	})
	if allocations != 0 {
		t.Fatalf("warmed sorted conversion allocations=%v want=0", allocations)
	}
	if !reflect.DeepEqual(avatars, wantAvatars) || !reflect.DeepEqual(tags, wantTags) {
		t.Fatalf("sorted conversion=%+v/%+v want=%+v/%+v", avatars, tags, wantAvatars, wantTags)
	}
	for index := range sorted {
		if avatars[index].PlayerID != sorted[index].PlayerID ||
			avatars[index].Position != sorted[index].Position ||
			avatars[index].Yaw != sorted[index].Yaw ||
			avatars[index].Pitch != sorted[index].Pitch {
			t.Fatalf("avatar %d=%+v presentation=%+v", index, avatars[index], sorted[index])
		}
		wantAnchor := sorted[index].Position.Add(mgl32.Vec3{0, 2.05, 0})
		if tags[index].PlayerID != sorted[index].PlayerID ||
			tags[index].Text != sorted[index].DisplayName ||
			tags[index].Anchor != wantAnchor {
			t.Fatalf("tag %d=%+v want ID/text/anchor=%v/%q/%v", index, tags[index],
				sorted[index].PlayerID, sorted[index].DisplayName, wantAnchor)
		}
	}

	avatars, tags = remoteRenderPresentationsSortedInto(avatars[:0], tags[:0], nil)
	if len(avatars) != 0 || len(tags) != 0 {
		t.Fatalf("empty conversion lengths=%d/%d want=0/0", len(avatars), len(tags))
	}
	avatars, tags = remoteRenderPresentationsSortedInto(avatars[:0], tags[:0], sorted)
	if !reflect.DeepEqual(avatars, wantAvatars) || !reflect.DeepEqual(tags, wantTags) {
		t.Fatalf("refill after empty retained stale data: %+v/%+v", avatars, tags)
	}
}

func TestRemoteRenderPresentationsReturnsIndependentSortedResults(t *testing.T) {
	presentations := presentationConversionFixture()
	source := append([]client.RemotePresentation(nil), presentations...)
	wantAvatars, wantTags := remoteRenderPresentations(presentations)
	wantAvatars = append([]render.Avatar(nil), wantAvatars...)
	wantTags = append([]render.NameTag(nil), wantTags...)
	firstAvatars, firstTags := remoteRenderPresentations(presentations)
	secondAvatars, secondTags := remoteRenderPresentations(presentations)

	firstAvatars[0].Position[0] = 99
	firstTags[0].Text = "mutated"
	if !reflect.DeepEqual(secondAvatars, wantAvatars) || !reflect.DeepEqual(secondTags, wantTags) {
		t.Fatalf("second wrapper results aliased first output: %+v/%+v", secondAvatars, secondTags)
	}
	if !reflect.DeepEqual(presentations, source) {
		t.Fatalf("wrapper mutated input: %+v want=%+v", presentations, source)
	}
}

func BenchmarkRemotePresentationConversion(b *testing.B) {
	players := client.NewRemotePlayers()
	scenario := newMultiplayerBenchmarkScenario()
	for _, spawn := range scenario.Spawns {
		if err := players.Apply(spawn); err != nil {
			b.Fatalf("Apply spawn: %v", err)
		}
	}
	presentations := make([]client.RemotePresentation, 0, len(scenario.Spawns))
	avatars := make([]render.Avatar, 0, len(scenario.Spawns))
	tags := make([]render.NameTag, 0, len(scenario.Spawns))
	presentations = players.AppendPresentations(presentations)
	avatars, tags = remoteRenderPresentationsSortedInto(avatars, tags, presentations)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		presentations = players.AppendPresentations(presentations[:0])
		avatars, tags = remoteRenderPresentationsSortedInto(avatars[:0], tags[:0], presentations)
	}
	b.StopTimer()
	presentationBenchmarkPresentations = presentations
	presentationBenchmarkAvatars = avatars
	presentationBenchmarkTags = tags
}

func presentationConversionFixture() []client.RemotePresentation {
	names := [...]string{"星野", "月河", "云山", "海界", "星河", "月海", "云野"}
	presentations := make([]client.RemotePresentation, len(names))
	for index := range presentations {
		id := len(presentations) - index
		presentations[index] = client.RemotePresentation{
			PlayerID:    benchmarkPlayerID(id),
			DisplayName: names[id-1],
			Position:    mgl32.Vec3{float32(id), float32(id) * 0.5, -float32(id)},
			Yaw:         float32(id) * 0.1,
			Pitch:       -float32(id) * 0.01,
		}
	}
	return presentations
}
