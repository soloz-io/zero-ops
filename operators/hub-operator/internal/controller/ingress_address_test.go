package controller

import "testing"

// The address is a host, never host:port -- external-dns publishes an A record.
func TestControlPlaneHostIsExtractedWithoutThePort(t *testing.T) {
	cases := []struct {
		name, config, want string
		wantErr            bool
	}{
		{
			name:   "the real thing, as kubeadm writes it",
			config: "apiVersion: kubeadm.k8s.io/v1beta3\nclusterName: acme-hub\ncontrolPlaneEndpoint: 65.109.41.89:6443\nkind: ClusterConfiguration\n",
			want:   "65.109.41.89",
		},
		{name: "a hostname endpoint", config: "controlPlaneEndpoint: hub.example.test:6443", want: "hub.example.test"},
		{name: "quoted", config: `controlPlaneEndpoint: "65.109.41.89:6443"`, want: "65.109.41.89"},
		{name: "indented under a parent", config: "spec:\n  controlPlaneEndpoint: 10.0.0.5:6443\n", want: "10.0.0.5"},
		{name: "no port", config: "controlPlaneEndpoint: 65.109.41.89", want: "65.109.41.89"},
		// IPv6 is bracketed: the last colon is inside the literal, so naive
		// splitting would return "[2001:db8::1" and publish a broken record.
		{name: "IPv6", config: "controlPlaneEndpoint: [2001:db8::1]:6443", want: "2001:db8::1"},

		{name: "absent", config: "clusterName: acme-hub\n", wantErr: true},
		{name: "empty value", config: "controlPlaneEndpoint:\n", wantErr: true},
		{name: "empty config", config: "", wantErr: true},
		{name: "unterminated IPv6", config: "controlPlaneEndpoint: [2001:db8::1:6443", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := controlPlaneHost(tc.config)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// A near-miss key must not be mistaken for the endpoint.
func TestOnlyTheControlPlaneEndpointFieldIsRead(t *testing.T) {
	got, err := controlPlaneHost("localAPIEndpoint:\n  advertiseAddress: 10.0.0.9\n")
	if err == nil {
		t.Errorf("localAPIEndpoint was read as the control-plane endpoint: %q", got)
	}
}
