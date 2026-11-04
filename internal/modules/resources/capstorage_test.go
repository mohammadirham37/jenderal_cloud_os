package resources

import "testing"

func TestCapDefaultStorage(t *testing.T) {
	const gib = int64(1) << 30
	cases := []struct {
		def   int64
		capMB int
		want  int64
	}{
		{10 * gib, 0, 10 * gib},
		{10 * gib, 2048, 2 * gib},
		{1 * gib, 2048, 1 * gib},
		{10 * gib, 1, 256 << 20},
	}
	for _, c := range cases {
		if got := CapDefaultStorage(c.def, c.capMB); got != c.want {
			t.Errorf("CapDefaultStorage(%d, %d) = %d, want %d", c.def, c.capMB, got, c.want)
		}
	}
}
