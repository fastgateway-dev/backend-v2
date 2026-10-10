package services

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

type fakeStreamCaps struct{ streams bool }

func (f fakeStreamCaps) Has(context.Context, uuid.UUID, string) bool { return f.streams }

func TestStreamCreate_RejectedWhenStreamsUnsupported(t *testing.T) {
	s := &StreamService{} // zero-value is enough to reach the guard
	s.SetCapabilities(fakeStreamCaps{streams: false})
	_, err := s.Create(uuid.New(), CreateStreamInput{}, &models.User{})
	if !errors.Is(err, ErrStreamsUnsupported) {
		t.Fatalf("expected ErrStreamsUnsupported, got %v", err)
	}
}
