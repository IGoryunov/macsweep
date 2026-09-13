package report

import "testing"

func TestDisplayPath(t *testing.T) {
	cases := map[string]string{
		"/Users/x":              "~",
		"/Users/x/Library":      "~/Library",
		"/Users/xy/Library":     "/Users/xy/Library",
		"/opt/thing":            "/opt/thing",
		"/Users/x/~/weird":      "~/~/weird",
		"/Users/x/with space/a": "~/with space/a",
	}
	for in, want := range cases {
		if got := DisplayPath(in, "/Users/x/"); got != want {
			t.Errorf("DisplayPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{0: "0 B", 1023: "1023 B", 4096: "4 KB", 50 << 20: "50 MB", 3 << 30: "3.0 GB"}
	for in, want := range cases {
		if got := HumanSize(in); got != want {
			t.Errorf("HumanSize(%d) = %q, want %q", in, got, want)
		}
	}
}
