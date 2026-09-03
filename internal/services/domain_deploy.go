// DomainService: applying domain-level policy to the cluster.
//
// The BackendTrafficPolicy and EnvoyExtensionPolicy attached to a domain's
// Gateway, and the ReferenceGrant sync that keeps cross-namespace references
// legal as a project's domain namespaces change. Phase 2F Task 3.

package services

import (
	"context"
	"fmt"
	"log"

	"github.com/fastgateway-dev/backend-v2/internal/cluster"
	"github.com/fastgateway-dev/backend-v2/internal/domainplan"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

// getProjectDomainNamespaces returns the unique namespaces used by domains in a project.
// Always includes FastGatewayNamespace.
func (s *DomainService) getProjectDomainNamespaces(projectID uuid.UUID) ([]string, error) {
	domains, _, err := s.domainRepo.ListByProjectID(projectID, 1, 10000, "", "", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list domains for namespace collection: %w", err)
	}

	seen := map[string]bool{kubernetes.FastGatewayNamespace: true}
	namespaces := []string{kubernetes.FastGatewayNamespace}
	for _, d := range domains {
		if !seen[d.Namespace] {
			namespaces = append(namespaces, d.Namespace)
			seen[d.Namespace] = true
		}
	}
	return namespaces, nil
}

// syncReferenceGrants updates all ReferenceGrants in backend namespaces to allow
// references from all domain namespaces. Also creates a ReferenceGrant in
// fastgateway-system for cross-namespace secret access when non-default domain
// namespaces exist.
func (s *DomainService) syncReferenceGrants(projectID uuid.UUID) {
	ctx := context.Background()

	domainNamespaces, err := s.getProjectDomainNamespaces(projectID)
	if err != nil {
		log.Printf("Failed to get domain namespaces for ReferenceGrant sync: %v", err)
		return
	}

	// Get all backend namespaces
	backendNamespaces, err := s.projectNamespaceRepo.ListByProjectID(projectID)
	if err != nil {
		log.Printf("Failed to list backend namespaces for ReferenceGrant sync: %v", err)
		return
	}

	// Update ReferenceGrant in each backend namespace, honoring capabilities.
	for _, bns := range backendNamespaces {
		toKinds := models.ReferenceGrantKindsForCapabilities(bns.Capabilities)
		rgName := generateReferenceGrantName(projectID, bns.Namespace)
		if len(toKinds) == 0 {
			// Namespace has no target capabilities — make sure no stale RG lingers.
			_ = s.k8sRefGrants.DeleteReferenceGrant(ctx, projectID, bns.Namespace, rgName)
			continue
		}
		rgConfig := &cluster.ReferenceGrantConfig{
			Name:           rgName,
			FromNamespaces: domainNamespaces,
			ToNamespace:    bns.Namespace,
			ToKinds:        toKinds,
		}
		if err := s.k8sRefGrants.RecreateReferenceGrant(ctx, projectID, rgConfig); err != nil {
			log.Printf("Failed to sync ReferenceGrant in %s: %v", bns.Namespace, err)
		}
	}

	// Handle cross-namespace secret access: if there are domain namespaces other
	// than fastgateway-system, create a ReferenceGrant IN fastgateway-system
	// allowing those namespaces to reference secrets there.
	nonDefaultNamespaces := make([]string, 0)
	for _, ns := range domainNamespaces {
		if ns != kubernetes.FastGatewayNamespace {
			nonDefaultNamespaces = append(nonDefaultNamespaces, ns)
		}
	}

	shortID := projectID.String()[:8]
	secretsRGName := fmt.Sprintf("fastgateway-%s-secrets", shortID)

	if len(nonDefaultNamespaces) > 0 {
		rgConfig := &cluster.ReferenceGrantConfig{
			Name:           secretsRGName,
			FromNamespaces: nonDefaultNamespaces,
			ToNamespace:    kubernetes.FastGatewayNamespace,
			ToKinds:        []string{"Secret"},
		}
		if err := s.k8sRefGrants.RecreateReferenceGrant(ctx, projectID, rgConfig); err != nil {
			log.Printf("Failed to sync secrets ReferenceGrant in fastgateway-system: %v", err)
		}
	} else {
		// No non-default namespaces, clean up the secrets ReferenceGrant if it exists
		_ = s.k8sRefGrants.DeleteReferenceGrant(ctx, projectID, kubernetes.FastGatewayNamespace, secretsRGName)
	}
}

// applyDomainBackendTrafficPolicy saves and deploys or deletes the domain-level BTP
func (s *DomainService) applyDomainBackendTrafficPolicy(ctx context.Context, domain *models.Domain, btpConfig *models.BackendTrafficPolicyConfig) error {
	btpName := domain.K8sGatewayName + "-btp"

	if btpConfig == nil || btpConfig.IsEmpty() {
		// Delete from DB and K8s
		_ = s.btpRepo.DeleteByDomainID(domain.ID)
		_ = s.k8sPolicies.DeleteBackendTrafficPolicy(ctx, domain.ProjectID, domain.Namespace, btpName)
		return nil
	}

	// Save to DB
	policy := &models.BackendTrafficPolicy{
		DomainID:  &domain.ID,
		ProjectID: domain.ProjectID,
		Config:    *btpConfig,
	}
	if err := s.btpRepo.Upsert(policy); err != nil {
		return fmt.Errorf("failed to save domain BTP: %w", err)
	}

	// Build K8s config and deploy
	k8sConfig := domainplan.BuildBackendTrafficPolicyConfig(domain, btpConfig)
	if k8sConfig == nil {
		return nil
	}
	if err := s.k8sPolicies.UpdateBackendTrafficPolicy(ctx, domain.ProjectID, k8sConfig); err != nil {
		return fmt.Errorf("failed to apply domain BackendTrafficPolicy to Kubernetes: %w", err)
	}
	return nil
}

// applyDomainEnvoyExtensionPolicy saves and deploys or deletes the domain-level extension policy
func (s *DomainService) applyDomainEnvoyExtensionPolicy(ctx context.Context, domain *models.Domain, extConfig *models.EnvoyExtensionPolicyConfig) error {
	eepName := domain.K8sGatewayName + "-eep"
	extProcBackendName := kubernetes.GenerateExtProcBackendNameForDomain(domain.K8sGatewayName)

	if extConfig == nil || extConfig.IsEmpty() {
		// Delete from DB and K8s
		_ = s.extPolicyRepo.DeleteByDomainID(domain.ID)
		_ = s.k8sBackends.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extProcBackendName)
		_ = s.k8sPolicies.DeleteEnvoyExtensionPolicy(ctx, domain.ProjectID, domain.Namespace, eepName)
		return nil
	}

	// Save to DB
	policy := &models.EnvoyExtensionPolicy{
		DomainID:  &domain.ID,
		ProjectID: domain.ProjectID,
		Config:    *extConfig,
	}
	if err := s.extPolicyRepo.Upsert(policy); err != nil {
		return fmt.Errorf("failed to save domain extension policy: %w", err)
	}

	// Handle ext-proc Backend CRD lifecycle
	//
	// Deliberately NOT extracted to a shared builder (Phase 2H, spec §6).
	// The two ExtProcBackendConfig sites differ in owner identity -- this one
	// sets DomainID with an empty RouteID; the route path sets RouteID -- and
	// object construction is already shared via kubernetes.BuildExtProcBackend.
	// A parameterised builder would encode two owner semantics in one
	// signature for no reduction in size.
	if extConfig.ExtProc != nil {
		backendConfig := &kubernetes.ExtProcBackendConfig{
			Name:      extProcBackendName,
			Namespace: domain.Namespace,
			GatewayID: domain.ID.String(),
			RouteID:   "",
			DomainID:  domain.ID.String(),
			Service: kubernetes.ExtProcBackendRefPolicyConfig{
				Name:      extConfig.ExtProc.BackendRef.Name,
				Namespace: extConfig.ExtProc.BackendRef.Namespace,
				Port:      extConfig.ExtProc.BackendRef.Port,
			},
		}
		backend := kubernetes.BuildExtProcBackend(backendConfig)
		if backend != nil {
			if err := s.k8sBackends.UpdateBackendUnstructured(ctx, domain.ProjectID, backend); err != nil {
				return fmt.Errorf("failed to create/update domain ext-proc Backend: %w", err)
			}
		}
	} else {
		_ = s.k8sBackends.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extProcBackendName)
	}

	// Build K8s config and deploy
	k8sConfig := domainplan.BuildEnvoyExtensionPolicyConfig(domain, extConfig)
	if k8sConfig == nil {
		return nil
	}
	extPolicy := kubernetes.BuildEnvoyExtensionPolicy(k8sConfig)
	if extPolicy == nil {
		return nil
	}
	if err := s.k8sPolicies.UpdateEnvoyExtensionPolicy(ctx, domain.ProjectID, extPolicy); err != nil {
		return fmt.Errorf("failed to apply domain EnvoyExtensionPolicy to Kubernetes: %w", err)
	}
	return nil
}
