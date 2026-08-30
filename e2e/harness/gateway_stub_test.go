//go:build e2e

package harness

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/fastgateway-dev/backend-v2/e2e/testdata/pb/echo"
)

// TestEchoStub_MarshalRoundTrip is a compile-only smoke check for the
// generated echo stub (see e2e/testdata/pb/echo and the `protos` Makefile
// target): building a typed request/response needs no hand-encoded
// protobuf bytes, unlike Gateway.GRPC's raw-bytes path -- construct a
// message, round-trip it through the wire format via proto.Marshal /
// proto.Unmarshal exactly as Gateway.GRPCTyped does internally, and
// confirm the field survives. No cluster required.
func TestEchoStub_MarshalRoundTrip(t *testing.T) {
	want := &echo.Message{Body: "hi"}

	wire, err := proto.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got := &echo.Message{}
	if err := proto.Unmarshal(wire, got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.GetBody() != want.GetBody() {
		t.Fatalf("got Body=%q, want %q", got.GetBody(), want.GetBody())
	}
}
