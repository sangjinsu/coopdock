package admin

import (
	"strings"
	"testing"
)

const testToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestViewerTokenBounds(t *testing.T) {
	cases := []struct {
		token string
		valid bool
	}{{"", true}, {testToken, true}, {strings.Repeat("~", 256), true}, {strings.Repeat("a", 31), false}, {strings.Repeat("a", 257), false}, {testToken + " ", false}, {" " + testToken, false}, {testToken + "\x00", false}, {testToken + "한", false}, {strings.Repeat("a", 31) + "\x7f", false}}
	for i, tc := range cases {
		err := (Config{ViewerToken: tc.token}).Validate()
		if (err == nil) != tc.valid {
			t.Errorf("case %d valid=%v mismatch", i, tc.valid)
		}
		if err != nil && (err.Error() != "Invalid ADMIN_VIEWER_TOKEN." || strings.Contains(err.Error(), tc.token)) {
			t.Errorf("case %d unsafe validation error", i)
		}
		if (Config{ViewerToken: tc.token}).Enabled() != (tc.token != "") {
			t.Errorf("case %d enabled mismatch", i)
		}
	}
}
