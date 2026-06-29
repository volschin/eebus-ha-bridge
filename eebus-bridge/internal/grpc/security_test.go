package grpc

import "testing"

func TestIsLoopbackBind(t *testing.T) {
	cases := []struct {
		bind string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.0.0.2", true},
		{"::1", true},
		{"localhost", true},
		{"0.0.0.0", false},
		{"::", false},
		{"", false},
		{"192.168.68.119", false},
		{"10.0.0.5", false},
		{"example.com", false},
	}
	for _, tc := range cases {
		if got := isLoopbackBind(tc.bind); got != tc.want {
			t.Errorf("isLoopbackBind(%q) = %v, want %v", tc.bind, got, tc.want)
		}
	}
}

func TestRegisterPushServicesGatedOnLoopback(t *testing.T) {
	grid := NewGridService(nil)
	viz := NewVisualizationService(nil, nil)

	t.Run("loopback registers", func(t *testing.T) {
		srv := NewServer("127.0.0.1", 0, false)
		if !RegisterPushServices(srv, "127.0.0.1", grid, viz) {
			t.Fatal("expected push services to register on loopback bind")
		}
	})

	t.Run("exposed bind refused", func(t *testing.T) {
		srv := NewServer("0.0.0.0", 0, false)
		if RegisterPushServices(srv, "0.0.0.0", grid, viz) {
			t.Fatal("expected push services to be refused on routable bind")
		}
	})
}
