package vcenter

import "testing"

func TestNormalizeEndpoint(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"host only → append /sdk", "https://10.121.160.210", "https://10.121.160.210/sdk"},
		{"trailing slash → /sdk", "https://10.121.160.210/", "https://10.121.160.210/sdk"},
		{"explicit /sdk → unchanged", "https://10.121.160.210/sdk", "https://10.121.160.210/sdk"},
		{"with port → append /sdk", "https://vc.example.local:8443", "https://vc.example.local:8443/sdk"},
		{"custom path → unchanged", "https://vc.example.local/proxy/vc1/sdk", "https://vc.example.local/proxy/vc1/sdk"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalizeEndpoint(c.in); got != c.want {
				t.Fatalf("normalizeEndpoint(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
