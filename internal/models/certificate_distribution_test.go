package models

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCertificateDistribution(t *testing.T) {
	t.Run("TableName returns certificate_distributions", func(t *testing.T) {
		cd := CertificateDistribution{}
		if cd.TableName() != "certificate_distributions" {
			t.Errorf("expected table name 'certificate_distributions', got %q", cd.TableName())
		}
	})

	t.Run("zero-value Status is pending", func(t *testing.T) {
		cd := CertificateDistribution{}
		if cd.Status != "" {
			t.Errorf("expected zero-value Status to be empty string, got %q", cd.Status)
		}
	})

	t.Run("construct and verify fields", func(t *testing.T) {
		certID := uuid.New()
		projID := uuid.New()
		now := time.Now()

		cd := CertificateDistribution{
			ID:                    uuid.New(),
			ManagedCertificateID:  certID,
			ProjectID:             projID,
			Status:                CertDistStatusPending,
			LastPushedFingerprint: "abc123",
			Message:               "test message",
			LastSyncedAt:          &now,
			CreatedAt:             now,
			UpdatedAt:             now,
		}

		if cd.ManagedCertificateID != certID {
			t.Error("ManagedCertificateID not set correctly")
		}
		if cd.ProjectID != projID {
			t.Error("ProjectID not set correctly")
		}
		if cd.Status != CertDistStatusPending {
			t.Errorf("expected Status to be 'pending', got %q", cd.Status)
		}
		if cd.LastPushedFingerprint != "abc123" {
			t.Error("LastPushedFingerprint not set correctly")
		}
		if cd.Message != "test message" {
			t.Error("Message not set correctly")
		}
		if cd.LastSyncedAt == nil || *cd.LastSyncedAt != now {
			t.Error("LastSyncedAt not set correctly")
		}
	})
}
