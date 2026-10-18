package config

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestGameIDs(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want []string
	}{{"default", []string{"default"}}, {" game-a , game-b ", []string{"game-a", "game-b"}}} {
		t.Run(tc.raw, func(t *testing.T) {
			t.Setenv("GAME_IDS", tc.raw)
			c, err := Load()
			if err != nil || !reflect.DeepEqual(c.Room.GameIDs, tc.want) {
				t.Fatalf("GameIDs=%v err=%v", c.Room.GameIDs, err)
			}
		})
	}
	ids := make([]string, 33)
	for i := range ids {
		ids[i] = fmt.Sprintf("g%d", i)
	}
	for _, raw := range []string{"", "a,", "a,,b", "a,a", "A", "한", "-a", strings.Repeat("a", 65), strings.Join(ids, ",")} {
		t.Run("invalid/"+raw, func(t *testing.T) {
			t.Setenv("GAME_IDS", raw)
			_, err := Load()
			if err == nil || err.Error() != "Invalid GAME_IDS." {
				t.Fatalf("invalid registry accepted or reflected input: %v", err)
			}
		})
	}
}
