package profile

import "testing"

func FuzzDecodeProfile(f *testing.F) {
	f.Add([]byte(`{"version":1,"player_id":"00112233-4455-4677-8899-aabbccddeeff","display_name":"Chen"}`))
	f.Add([]byte("{"))
	f.Fuzz(func(t *testing.T, encoded []byte) {
		got, err := decodeProfile(encoded)
		if err != nil {
			return
		}
		if got.Version != CurrentVersion || !got.PlayerID.Valid() || got.DisplayName == "" {
			t.Fatalf("accepted invalid profile: %+v", got)
		}
	})
}
