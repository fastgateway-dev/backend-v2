package services

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type fakeVersionGetter struct {
	info *VersionInfo
	err  error
}

func (f fakeVersionGetter) Get(context.Context, uuid.UUID, bool) (*VersionInfo, error) {
	return f.info, f.err
}

func infoWith(eg, gw string) *VersionInfo {
	return &VersionInfo{
		EnvoyGateway: ProbeResult{Version: eg, Detected: eg != ""},
		GatewayAPI:   ProbeResult{Version: gw, Detected: gw != ""},
	}
}

func TestCapabilityService_Has(t *testing.T) {
	ctx, id := context.Background(), uuid.New()
	tests := []struct {
		name               string
		getter             fakeVersionGetter
		streams, l4RouteV1 bool
	}{
		{"eg19 gw16", fakeVersionGetter{info: infoWith("1.9.1", "1.6.1")}, true, true},
		{"eg18 gw15", fakeVersionGetter{info: infoWith("1.8.4", "1.5.1")}, true, false},
		{"eg17 gw14", fakeVersionGetter{info: infoWith("1.7.5", "1.4.1")}, false, false},
		{"probe error -> defaults", fakeVersionGetter{err: errors.New("unreachable")}, true, true},
		{"nil info -> defaults", fakeVersionGetter{info: nil}, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewCapabilityService(tc.getter)
			if got := s.Has(ctx, id, "streams"); got != tc.streams {
				t.Errorf("streams = %v, want %v", got, tc.streams)
			}
			if got := s.Has(ctx, id, "l4RouteV1"); got != tc.l4RouteV1 {
				t.Errorf("l4RouteV1 = %v, want %v", got, tc.l4RouteV1)
			}
		})
	}
}

func TestCapabilityService_EvaluateExposedOnly(t *testing.T) {
	s := NewCapabilityService(fakeVersionGetter{info: infoWith("1.9.1", "1.6.1")})
	got := s.Evaluate(context.Background(), uuid.New())
	if _, ok := got["streams"]; !ok {
		t.Error("Evaluate must include exposed capability 'streams'")
	}
	if _, ok := got["l4RouteV1"]; ok {
		t.Error("Evaluate must NOT include internal capability 'l4RouteV1'")
	}
}
