// Package certdist implements the Phase 3a certificate distribution
// reconcile controller.
//
// Once cert-manager (or whichever issuer) writes an issued leaf
// certificate/key pair into a Secret on the control-plane cluster, that
// material still needs to land in each project's own tenant cluster, in the
// namespace Envoy Gateway's Gateway resource lives in, before it is actually
// usable for TLS termination. The Distributor in this package is the
// controller that does that copy, on a recurring basis, and records the
// outcome (synced / pending / error) in the certificate_distributions table
// so the sync state of any given certificate is independently observable
// from the ManagedCertificate row itself.
//
// Only one replica of the API process should ever run this controller at a
// time -- concurrent pushes to the same tenant Secret are wasteful at best
// and racy at worst -- so every reconciliation pass first checks a
// leaderlock.Gate and does nothing at all unless this replica currently
// holds the elected-leader advisory lock.
//
// The Distributor depends on nothing but narrow interfaces (CertLister,
// DistRepo, SourceReader, TenantWriter, CertUpdater, leaderlock.Gate) so it
// can be exercised in unit tests with trivial fakes instead of a real
// database or Kubernetes cluster. Concrete implementations of SourceReader
// (wrapping cluster.ControlPlaneClient) and CertUpdater (a small repository
// method) are wired up separately.
package certdist

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/fastgateway-dev/backend-v2/internal/cluster"
	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/leaderlock"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// tenantSecretNamespace is the namespace, inside each project's own tenant
// cluster, that the distribution controller pushes issued leaf-certificate
// Secrets into. Phase 3a always targets the shared Gateway namespace
// (Ruling 3a-P2); per-domain/per-listener namespace wiring is a Phase 3b
// concern and does not change this controller.
const tenantSecretNamespace = "fastgateway-system"

// CertLister lists managed certificates whose issued material might need
// (re)distribution. Satisfied by *repository.ManagedCertificateRepository.
type CertLister interface {
	ListByStatuses(statuses []models.ManagedCertStatus) ([]models.ManagedCertificate, error)
}

// DistRepo persists the per-certificate distribution row that tracks
// tenant-cluster sync status independently of the ManagedCertificate row
// itself. Satisfied by *repository.CertificateDistributionRepository.
type DistRepo interface {
	Upsert(cd *models.CertificateDistribution) error
	GetByCertificateID(certID uuid.UUID) (*models.CertificateDistribution, error)
}

// SourceReader reads the leaf certificate/key pair that the issuer has
// written into the control-plane cluster once issuance completes. found is
// false, with a nil error, when the Secret simply doesn't exist yet (the
// certificate is still issuing) -- that is an expected state, not an error.
//
// A concrete implementation wraps cluster.ControlPlaneClient.Get with
// kubernetes.CoreSecretGVR and base64-decodes data["tls.crt"]/["tls.key"].
type SourceReader interface {
	ReadLeafSecret(ctx context.Context, name string) (crt, key []byte, found bool, err error)
}

// TenantWriter lands issued leaf material in a project's tenant cluster, and
// lets the controller inspect what's already there for drift detection /
// self-healing. Satisfied by *cluster.Client.
type TenantWriter interface {
	CreateOrUpdateTLSSecret(ctx context.Context, projectID uuid.UUID, namespace, name string, crt, key []byte) error
	GetSecretData(ctx context.Context, projectID uuid.UUID, namespace, name, key string) ([]byte, error)
}

// CertUpdater persists the fingerprint/notAfter observed on a
// ManagedCertificate's issued leaf material, once a distribution push
// succeeds.
type CertUpdater interface {
	SetIssuedMeta(certID uuid.UUID, fingerprint string, notAfter *time.Time) error
}

// Deps are the Distributor's collaborators. Every field is required; New
// panics if any is nil since a missing dependency is a wiring bug, not a
// runtime condition to recover from.
type Deps struct {
	Gate         leaderlock.Gate
	Lister       CertLister
	DistRepo     DistRepo
	Source       SourceReader
	TenantWriter TenantWriter
	CertUpdater  CertUpdater
	Config       *config.Config
}

// Distributor is the Phase 3a certificate distribution reconcile
// controller.
type Distributor struct {
	deps Deps
}

// New builds a Distributor from deps. It panics if any dependency (or
// Config) is nil.
func New(deps Deps) *Distributor {
	switch {
	case deps.Gate == nil:
		panic("certdist: Gate dependency is nil")
	case deps.Lister == nil:
		panic("certdist: Lister dependency is nil")
	case deps.DistRepo == nil:
		panic("certdist: DistRepo dependency is nil")
	case deps.Source == nil:
		panic("certdist: Source dependency is nil")
	case deps.TenantWriter == nil:
		panic("certdist: TenantWriter dependency is nil")
	case deps.CertUpdater == nil:
		panic("certdist: CertUpdater dependency is nil")
	case deps.Config == nil:
		panic("certdist: Config dependency is nil")
	}
	return &Distributor{deps: deps}
}

// Reconcile runs one full reconciliation pass over every managed certificate
// that has, or may soon have, issued leaf material: statuses "ready"
// (already issued at least once) and "issuing" (issuance may have completed
// since the previous pass). If this replica is not the elected leader,
// Reconcile returns immediately without reading or writing anything --
// exactly one replica should ever perform distribution at a time.
//
// A single certificate's failure never aborts the pass: reconcileOne is run
// through a panic-recovering wrapper for every certificate, and its errors
// are logged rather than propagated, so one bad certificate can't block
// distribution for the rest.
func (d *Distributor) Reconcile(ctx context.Context) error {
	if !d.deps.Gate.IsLeader() {
		return nil
	}

	certs, err := d.deps.Lister.ListByStatuses([]models.ManagedCertStatus{
		models.ManagedCertStatusReady,
		models.ManagedCertStatusIssuing,
	})
	if err != nil {
		return fmt.Errorf("certdist: listing certificates: %w", err)
	}

	for _, cert := range certs {
		if !d.deps.Gate.IsLeader() {
			// Lost leadership mid-pass: stop so the newly-elected leader is
			// the only writer. Remaining certs reconcile on the next tick.
			break
		}
		d.reconcileOneSafely(ctx, cert)
	}

	return nil
}

// reconcileOneSafely runs reconcileOne for a single certificate, recovering
// from any panic so that a bug triggered by one certificate's data can never
// take down the rest of the reconciliation pass.
func (d *Distributor) reconcileOneSafely(ctx context.Context, cert models.ManagedCertificate) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("certdist: recovered panic reconciling certificate %s: %v", cert.ID, r)
		}
	}()

	if err := d.reconcileOne(ctx, cert); err != nil {
		log.Printf("certdist: reconciling certificate %s: %v", cert.ID, err)
	}
}

// reconcileOne reconciles a single managed certificate's distribution
// state: it reads the certificate's issued leaf material from the control
// plane, decides whether the tenant cluster's copy needs a (re)push, and
// records the outcome in the distribution row. The returned error is purely
// for callers/tests to observe -- reconcileOneSafely already logs it and
// treats it as non-fatal for the overall pass.
func (d *Distributor) reconcileOne(ctx context.Context, cert models.ManagedCertificate) error {
	// Load whatever we already know about this certificate's distribution
	// state up front, so error paths below can preserve fields (like
	// LastSyncedAt) that this pass doesn't otherwise touch instead of
	// clobbering them with zero values on Upsert. gorm.ErrRecordNotFound
	// means there's genuinely no row yet (dist stays nil, treated as new)
	// and is not an error condition.
	dist, err := d.deps.DistRepo.GetByCertificateID(cert.ID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		// Transient read error: bail out of this cert's reconcile WITHOUT
		// an upsert. Proceeding would let the error/pending paths below
		// overwrite the existing row's fingerprint/last-synced with zero
		// values via the OnConflict{UpdateAll} upsert. The next tick
		// retries cleanly.
		return fmt.Errorf("certdist: loading distribution state for %s: %w", cert.ID, err)
	}

	crt, key, found, err := d.deps.Source.ReadLeafSecret(ctx, cert.Config.SecretName)
	if err != nil {
		return d.upsertDist(cert, dist, models.CertDistStatusError, currentFingerprint(dist), fmt.Sprintf("reading source secret: %v", err), false)
	}
	if !found {
		// Issuance hasn't produced a Secret yet -- expected while a
		// certificate is still "issuing". Nothing to push, and this is not
		// an error condition.
		return d.upsertDist(cert, dist, models.CertDistStatusPending, currentFingerprint(dist), "source not issued yet", false)
	}

	fp := cluster.CertFingerprint(crt)

	if !d.needsPush(ctx, cert, dist, fp) {
		return nil
	}

	if err := d.deps.TenantWriter.CreateOrUpdateTLSSecret(ctx, cert.ProjectID, tenantSecretNamespace, cert.Config.SecretName, crt, key); err != nil {
		return d.upsertDist(cert, dist, models.CertDistStatusError, currentFingerprint(dist), fmt.Sprintf("pushing to tenant cluster: %v", err), false)
	}

	if err := d.upsertDist(cert, dist, models.CertDistStatusSynced, fp, "", true); err != nil {
		return err
	}

	if err := d.deps.CertUpdater.SetIssuedMeta(cert.ID, fp, parseNotAfter(crt)); err != nil {
		return fmt.Errorf("recording issued meta: %w", err)
	}

	return nil
}

// needsPush decides whether the tenant cluster's copy of a certificate's
// leaf material is stale, missing, or drifted, and therefore needs a
// (re)push. It first checks the distribution bookkeeping row; if that says
// we're already in sync at the current fingerprint, it self-heals by
// checking what's actually present in the tenant cluster, since an operator
// (or anything else) may have deleted or mutated the Secret directly and
// that must not be missed just because our bookkeeping still says "synced".
func (d *Distributor) needsPush(ctx context.Context, cert models.ManagedCertificate, dist *models.CertificateDistribution, fp string) bool {
	if dist == nil || dist.Status != models.CertDistStatusSynced || dist.LastPushedFingerprint != fp {
		return true
	}

	tenantCrt, err := d.deps.TenantWriter.GetSecretData(ctx, cert.ProjectID, tenantSecretNamespace, cert.Config.SecretName, "tls.crt")
	if err != nil {
		// Treat "can't confirm what's in the tenant cluster" (including
		// not-found) as "needs push" -- better to redundantly re-push than
		// to silently leave a tenant cluster without the cert it needs.
		return true
	}

	return cluster.CertFingerprint(tenantCrt) != fp
}

// upsertDist records this pass's outcome for a certificate's distribution
// row. It never writes key material -- only the fingerprint (a SHA-256
// digest, not the cert/key bytes themselves), status, and a human-readable
// message.
func (d *Distributor) upsertDist(cert models.ManagedCertificate, existing *models.CertificateDistribution, status models.CertDistStatus, fingerprint, message string, synced bool) error {
	cd := &models.CertificateDistribution{
		ManagedCertificateID:  cert.ID,
		ProjectID:             cert.ProjectID,
		Status:                status,
		LastPushedFingerprint: fingerprint,
		Message:               message,
	}

	if existing != nil {
		cd.ID = existing.ID
		cd.LastSyncedAt = existing.LastSyncedAt
	}

	if synced {
		now := time.Now()
		cd.LastSyncedAt = &now
	}

	if err := d.deps.DistRepo.Upsert(cd); err != nil {
		return fmt.Errorf("recording distribution status: %w", err)
	}

	return nil
}

// Run drives the reconciliation loop until ctx is cancelled. Callers should
// start it in its own goroutine, e.g. `go distributor.Run(ctx,
// cfg.CertDistributorInterval, notifyCh)`.
//
// Reconcile runs once immediately at start (so a freshly (re)started replica
// doesn't wait a full tick before its first pass), then again on every tick
// and every notifyCh wake. notifyCh is a LISTEN/NOTIFY-style nudge for
// faster-than-tick pickup of a just-approved certificate; it may be nil, in
// which case the loop simply never selects on it and the ticker alone drives
// reconciliation -- the ticker is Phase 3a's baseline self-heal, notifyCh is
// purely a latency optimization on top of it.
//
// A Reconcile error is logged, never fatal: the loop keeps running so a
// transient failure (a blip talking to Postgres or a tenant cluster) is
// simply retried on the next wake, exactly like Reconcile itself never lets
// one certificate's failure abort the rest of a pass. When ctx is cancelled,
// Run stops the ticker and returns -- no goroutine is leaked.
func (d *Distributor) Run(ctx context.Context, tick time.Duration, notifyCh <-chan struct{}) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	d.runOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.runOnce(ctx)
		case <-notifyCh:
			d.runOnce(ctx)
		}
	}
}

// runOnce runs a single Reconcile pass and logs its error, if any, rather
// than propagating it -- Run's loop must never exit just because one
// reconciliation pass failed.
func (d *Distributor) runOnce(ctx context.Context) {
	if err := d.Reconcile(ctx); err != nil {
		log.Printf("certdist: reconcile: %v", err)
	}
}

// currentFingerprint returns the last known-good pushed fingerprint from an
// existing distribution row, or "" if there isn't one. Used so an error
// path never clobbers a previously-synced fingerprint with a
// not-yet-successfully-pushed one.
func currentFingerprint(dist *models.CertificateDistribution) string {
	if dist == nil {
		return ""
	}
	return dist.LastPushedFingerprint
}

// parseNotAfter decodes a PEM-encoded leaf certificate and returns its
// NotAfter time, or nil if crt isn't a parseable PEM certificate.
func parseNotAfter(crt []byte) *time.Time {
	block, _ := pem.Decode(crt)
	if block == nil {
		return nil
	}

	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}

	notAfter := parsed.NotAfter
	return &notAfter
}
