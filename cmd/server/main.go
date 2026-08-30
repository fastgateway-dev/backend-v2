package main

import (
	_ "embed"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/database"
	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/services"

	"github.com/gin-gonic/gin"
)

//go:embed openapi.yaml
var openapiSpec []byte

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Set Gin mode
	if cfg.LogLevel == "debug" {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	// Run database migrations
	if err := database.RunMigrations(cfg.BuildDatabaseURL()); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Connect to database
	db, err := database.Connect(cfg.BuildDatabaseURL())
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	// Initialize repositories
	userRepo := repository.NewUserRepository(db)
	apiTokenRepo := repository.NewAPITokenRepository(db)
	projectRepo := repository.NewProjectRepository(db)
	teamRepo := repository.NewTeamRepository(db)
	domainRepo := repository.NewDomainRepository(db)
	routeRepo := repository.NewRouteRepository(db)
	approvalRepo := repository.NewUnifiedApprovalRepository(db)
	approvalPolicyRepo := repository.NewApprovalPolicyRepository(db)
	auditLogRepo := repository.NewAuditLogRepository(db)
	domainTemplateRepo := repository.NewDomainTemplateRepository(db)
	projectNamespaceRepo := repository.NewProjectNamespaceRepository(db)
	securityPolicyRepo := repository.NewSecurityPolicyRepository(db)
	backendTrafficPolicyRepo := repository.NewBackendTrafficPolicyRepository(db)
	envoyExtensionPolicyRepo := repository.NewEnvoyExtensionPolicyRepository(db)
	wafPolicyRepo := repository.NewWafPolicyRepository(db)
	clientRepo := repository.NewClientRepository(db)
	clientIPRepo := repository.NewClientIPRepository(db)
	clientHeaderRepo := repository.NewClientHeaderRepository(db)
	clientAttachmentRepo := repository.NewClientAttachmentRepository(db)
	domainSettingsRepo := repository.NewDomainSettingsRepository(db)
	presetRepo := repository.NewPresetRepository(db)
	ssoConfigRepo := repository.NewSSOConfigRepository(db)
	systemSettingsRepo := repository.NewSystemSettingsRepository(db)
	emailInviteRepo := repository.NewTeamEmailInviteRepository(db)
	commentRepo := repository.NewCommentRepository(db)
	notificationRepo := repository.NewNotificationRepository(db)
	routeVersionRepo := repository.NewRouteVersionRepository(db)
	approvalStageReviewRepo := repository.NewApprovalStageReviewRepository(db)

	// Initialize services
	authService := services.NewAuthService(userRepo, apiTokenRepo, cfg)
	userService := services.NewUserService(userRepo)

	// Seed default admin user (hashes password at runtime)
	if err := userService.SeedDefaultAdmin(cfg.AdminUsername, cfg.AdminPassword, cfg.AdminEmail); err != nil {
		log.Fatalf("Failed to seed default admin user: %v", err)
	}
	projectService := services.NewProjectService(projectRepo, cfg)
	k8sService := services.NewKubernetesService(projectService)
	projectService.SetKubernetesService(k8sService) // Set K8s service for prerequisite validation
	projectService.SetApprovalPolicyRepository(approvalPolicyRepo)
	projectService.SetPresetRepository(presetRepo) // Set preset repo for seeding built-in presets
	presetService := services.NewPresetService(presetRepo)
	teamService := services.NewTeamService(teamRepo, userRepo, presetRepo)
	domainService := services.NewDomainService(domainRepo, projectRepo, domainTemplateRepo, k8sService)
	domainService.SetDomainSettingsRepository(domainSettingsRepo)
	domainService.SetClientAttachmentRepository(clientAttachmentRepo) // Set client attachment repo for mTLS CA merging
	routeService := services.NewRouteService(routeRepo, approvalRepo, approvalPolicyRepo, domainRepo, teamRepo)
	routeService.SetKubernetesService(k8sService)                            // Set K8s service for route deployment
	routeService.SetProjectNamespaceRepository(projectNamespaceRepo)         // Set namespace repo for validation
	routeService.SetSecurityPolicyRepository(securityPolicyRepo)             // Set security policy repo for Envoy SecurityPolicy
	routeService.SetBackendTrafficPolicyRepository(backendTrafficPolicyRepo) // Set backend traffic policy repo for Envoy BackendTrafficPolicy
	routeService.SetEnvoyExtensionPolicyRepository(envoyExtensionPolicyRepo) // Set envoy extension policy repo for Lua/Wasm extensions
	routeService.SetWafPolicyRepository(wafPolicyRepo)                       // Set WAF policy repo for WAF/OWASP protection
	routeService.SetClientAttachmentRepository(clientAttachmentRepo)         // Set client attachment repo for IP allowlisting during deploy
	routeService.SetClientIPRepository(clientIPRepo)                         // Set client IP repo for IP allowlisting during deploy
	routeService.SetClientHeaderRepository(clientHeaderRepo)
	routeService.SetClientRepository(clientRepo)   // Set client repo for API key access during deploy
	routeService.SetDomainService(domainService)   // Set domain service for mTLS CA regeneration during deploy
	routeService.SetProjectRepository(projectRepo) // Set project repo for approval bypass
	routeVersionService := services.NewRouteVersionService(routeVersionRepo, routeRepo)
	routeVersionService.SetSecurityPolicyRepo(securityPolicyRepo)
	routeVersionService.SetBackendTrafficPolicyRepo(backendTrafficPolicyRepo)
	routeVersionService.SetEnvoyExtensionPolicyRepo(envoyExtensionPolicyRepo)
	routeVersionService.SetWafPolicyRepo(wafPolicyRepo)
	routeVersionService.SetRouteService(routeService)
	routeService.SetRouteVersionService(routeVersionService)
	approvalService := services.NewApprovalService(approvalRepo, approvalPolicyRepo, teamRepo, routeRepo, projectRepo, domainRepo, k8sService)
	approvalService.SetSecurityPolicyRepository(securityPolicyRepo)             // Set security policy repo for diff generation
	approvalService.SetBackendTrafficPolicyRepository(backendTrafficPolicyRepo) // Set backend traffic policy repo for diff generation
	approvalService.SetStageReviewRepository(approvalStageReviewRepo)           // Set stage review repo for multi-approver support
	auditService := services.NewAuditService(auditLogRepo)
	clientService := services.NewClientService(clientRepo, clientIPRepo, teamRepo)
	clientService.SetClientAttachmentRepository(clientAttachmentRepo) // Set client attachment repo for IP cascade
	clientService.SetRouteRepository(routeRepo)                       // Set route repo for IP cascade
	clientService.SetClientHeaderRepository(clientHeaderRepo)
	clientService.SetKubernetesService(k8sService) // Set K8s service for API key secrets
	clientAttachmentService := services.NewClientAttachmentService(clientAttachmentRepo, approvalRepo, approvalPolicyRepo, clientRepo, routeRepo, domainRepo, teamRepo, projectRepo)
	clientAttachmentService.SetDomainSettingsRepository(domainSettingsRepo)
	clientAttachmentService.SetStageReviewRepository(approvalStageReviewRepo)
	approvalService.SetClientAttachmentService(clientAttachmentService) // Wire for approval completion callbacks
	projectNamespaceService := services.NewProjectNamespaceService(projectNamespaceRepo, projectRepo, domainRepo, k8sService)
	projectVersionService := services.NewProjectVersionService(k8sService)

	// Initialize SSO service
	ssoService := services.NewSSOService(
		ssoConfigRepo,
		userRepo,
		teamRepo,
		emailInviteRepo,
		cfg,
	)
	authService.SetSSOService(ssoService)
	ssoService.SetTokenGenerator(authService.GenerateTokensForUser)

	// Initialize system settings service
	systemSettingsService := services.NewSystemSettingsService(systemSettingsRepo, cfg)
	authService.SetSystemSettingsService(systemSettingsService)
	ssoService.SetSystemSettingsService(systemSettingsService)

	// Initialize email invite service
	emailInviteService := services.NewTeamEmailInviteService(
		emailInviteRepo,
		userRepo,
		teamRepo,
	)

	// Initialize comment and notification services
	commentService := services.NewCommentService(commentRepo, notificationRepo, approvalRepo, teamRepo)
	notificationService := services.NewNotificationService(notificationRepo)

	// Initialize AI service
	aiService := services.NewAIService(cfg)
	domainTemplateService := services.NewDomainTemplateService(domainTemplateRepo, projectRepo, k8sService, aiService)
	domainService.SetDomainTemplateService(domainTemplateService)
	domainService.SetAIService(aiService)

	// Initialize middleware
	authMiddleware := middleware.NewAuthMiddleware(authService)
	permChecker := middleware.NewPermissionChecker(projectRepo, teamRepo)

	// Initialize handlers
	authHandler := handlers.NewAuthHandler(authService)
	userHandler := handlers.NewUserHandler(userService, auditService)
	projectHandler := handlers.NewProjectHandler(projectService, auditService, k8sService)
	metricsService := services.NewMetricsService(projectRepo, routeRepo, domainRepo, cfg)
	metricsHandler := handlers.NewMetricsHandler(metricsService)
	topologyService := services.NewTopologyService(
		domainRepo,
		routeRepo,
		clientAttachmentRepo,
		clientRepo,
		clientIPRepo,
		securityPolicyRepo,
		wafPolicyRepo,
		backendTrafficPolicyRepo,
		teamRepo,
		domainTemplateRepo,
	)
	topologyHandler := handlers.NewTopologyHandler(topologyService)
	openapiImportService := services.NewOpenAPIImportService()
	openapiImportHandler := handlers.NewOpenAPIImportHandler(openapiImportService)
	teamHandler := handlers.NewTeamHandler(teamService, teamRepo, auditService)
	domainTemplateHandler := handlers.NewDomainTemplateHandler(domainTemplateService, auditService, domainRepo)
	domainService.SetBackendTrafficPolicyRepository(backendTrafficPolicyRepo)
	domainService.SetEnvoyExtensionPolicyRepository(envoyExtensionPolicyRepo)
	domainService.SetProjectNamespaceRepository(projectNamespaceRepo)
	domainHandler := handlers.NewDomainHandler(domainService, auditService, permChecker, backendTrafficPolicyRepo, envoyExtensionPolicyRepo)
	routeHandler := handlers.NewRouteHandler(routeService, auditService, permChecker)
	routeVersionHandler := handlers.NewRouteVersionHandler(routeVersionService, auditService)
	approvalPolicyService := services.NewApprovalPolicyService(approvalPolicyRepo)
	approvalHandler := handlers.NewApprovalHandler(approvalService, auditService)
	approvalPolicyHandler := handlers.NewApprovalPolicyHandler(approvalPolicyService)
	auditHandler := handlers.NewAuditHandler(auditService)
	k8sHandler := handlers.NewKubernetesHandler(k8sService)
	clientHandler := handlers.NewClientHandler(clientService, auditService, teamRepo)
	clientAttachmentHandler := handlers.NewClientAttachmentHandler(clientAttachmentService, clientService, auditService, routeService, teamRepo)
	projectNamespaceHandler := handlers.NewProjectNamespaceHandler(projectNamespaceService, auditService, permChecker)
	projectVersionHandler := handlers.NewProjectVersionHandler(projectVersionService)
	presetHandler := handlers.NewPresetHandler(presetService, auditService)
	commentHandler := handlers.NewCommentHandler(commentService)
	notificationHandler := handlers.NewNotificationHandler(notificationService)

	// Wire email invite service into team handler
	teamHandler.SetEmailInviteService(emailInviteService)

	// Initialize SSO handler
	frontendURL := ""
	if len(cfg.CORSAllowedOrigins) > 0 {
		frontendURL = cfg.CORSAllowedOrigins[0]
	}
	ssoHandler := handlers.NewSSOHandler(ssoService, systemSettingsService, frontendURL)

	// Initialize AI handler
	aiHandler := handlers.NewAIHandler(aiService, approvalService, domainService)

	// Initialize system settings handler
	systemSettingsHandler := handlers.NewSystemSettingsHandler(systemSettingsService)

	// Initialize docs handler
	docsHandler := handlers.NewDocsHandler(openapiSpec)

	// Initialize permission handler
	permissionHandler := handlers.NewPermissionHandler(permChecker)

	// Setup router
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(middleware.Logger())
	// Health check
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// API v1 routes
	v1 := router.Group("/api/v1")
	{
		// Auth routes (public)
		auth := v1.Group("/auth")
		{
			auth.POST("/login", authHandler.Login)
			auth.POST("/refresh", authHandler.RefreshToken)

			// SSO routes (public - no auth required)
			sso := auth.Group("/sso")
			{
				sso.GET("/config", ssoHandler.GetPublicConfig)
				sso.GET("/authorize", ssoHandler.Authorize)
				sso.GET("/callback", ssoHandler.Callback)
			}
		}

		// Protected routes
		protected := v1.Group("")
		protected.Use(authMiddleware.Authenticate())
		{
			// Auth routes (protected)
			protectedAuth := protected.Group("/auth")
			{
				protectedAuth.POST("/logout", authHandler.Logout)
				protectedAuth.GET("/me", authHandler.GetCurrentUser)
				protectedAuth.PUT("/password", authHandler.ChangePassword)
				protectedAuth.GET("/tokens/capabilities", authHandler.GetAPITokenCapabilities)
				protectedAuth.GET("/tokens", authHandler.ListAPITokens)
				protectedAuth.POST("/tokens", authHandler.CreateAPIToken)
				protectedAuth.DELETE("/tokens/:tokenId", authHandler.RevokeAPIToken)
			}

			// Notifications (any authenticated user)
			notifications := protected.Group("/notifications")
			{
				notifications.GET("", notificationHandler.List)
				notifications.GET("/count", notificationHandler.CountUnread)
				notifications.PUT("/:notificationId/read", notificationHandler.MarkAsRead)
				notifications.PUT("/read-all", notificationHandler.MarkAllAsRead)
			}

			// API Documentation (any authenticated user)
			protected.GET("/docs/openapi.yaml", docsHandler.GetOpenAPISpec)

			// User management (Owner only)
			users := protected.Group("/users")
			users.Use(authMiddleware.RequireRole("owner"))
			{
				users.GET("", userHandler.List)
				users.POST("", userHandler.Create)
				users.GET("/:userId", userHandler.Get)
				users.PATCH("/:userId", userHandler.Update)
				users.DELETE("/:userId", userHandler.Delete)
			}

			// SSO settings (Owner only)
			ssoSettings := protected.Group("/settings/sso")
			ssoSettings.Use(authMiddleware.RequireRole("owner"))
			{
				ssoSettings.GET("", ssoHandler.GetConfig)
				ssoSettings.PUT("", ssoHandler.UpdateConfig)
				ssoSettings.DELETE("", ssoHandler.DisableSSO)
			}

			// System settings (Owner only)
			systemSettings := protected.Group("/settings/system")
			systemSettings.Use(authMiddleware.RequireRole("owner"))
			{
				systemSettings.GET("", systemSettingsHandler.Get)
				systemSettings.PUT("", systemSettingsHandler.Update)
			}

			// My Teams (any authenticated user)
			protected.GET("/my-teams", teamHandler.ListMyTeams)

			// Global Teams - list endpoint (Owner or users with project.teams permission)
			// This allows users who can assign teams to projects to see available teams
			protected.GET("/teams", teamHandler.List)

			// Global Teams - management endpoints (Owner only)
			teams := protected.Group("/teams")
			teams.Use(authMiddleware.RequireRole("owner"))
			{
				teams.POST("", teamHandler.Create)
				teams.GET("/:teamId", teamHandler.Get)
				teams.PATCH("/:teamId", teamHandler.Update)
				teams.DELETE("/:teamId", teamHandler.Delete)
				teams.GET("/:teamId/members", teamHandler.ListMembers)
				teams.POST("/:teamId/members", teamHandler.AddMember)
				teams.DELETE("/:teamId/members/:userId", teamHandler.RemoveMember)
				teams.GET("/:teamId/projects", teamHandler.ListTeamProjects)
				// Team email invites
				teams.POST("/:teamId/members/email", teamHandler.AddMemberByEmail)
				teams.GET("/:teamId/invites", teamHandler.ListInvites)
				teams.DELETE("/:teamId/invites/:inviteId", teamHandler.DeleteInvite)
			}

			// Global Clients (authenticated users)
			clients := protected.Group("/clients")
			{
				clients.GET("", clientHandler.List)
				clients.POST("", clientHandler.Create)
				clients.GET("/:clientId", clientHandler.Get)
				clients.PATCH("/:clientId", clientHandler.Update)
				clients.DELETE("/:clientId", clientHandler.Delete)
				clients.GET("/:clientId/ips", clientHandler.ListIPs)
				clients.POST("/:clientId/ips", clientHandler.AddIP)
				clients.DELETE("/:clientId/ips/:ipId", clientHandler.RemoveIP)
				clients.GET("/:clientId/headers", clientHandler.ListHeaders)
				clients.POST("/:clientId/headers", clientHandler.AddHeader)
				clients.DELETE("/:clientId/headers/:headerId", clientHandler.RemoveHeader)
				// Method authorization routes
				clients.PUT("/:clientId/methods", clientHandler.SetAllowedMethods)
				// API Key routes
				clients.POST("/:clientId/api-key", clientHandler.GenerateAPIKey)
				clients.DELETE("/:clientId/api-key", clientHandler.RevokeAPIKey)
				// JWT routes
				clients.POST("/:clientId/jwt", clientHandler.ConfigureJWT)
				clients.PUT("/:clientId/jwt", clientHandler.UpdateJWT)
				clients.DELETE("/:clientId/jwt", clientHandler.RemoveJWT)
				// mTLS routes
				clients.PUT("/:clientId/mtls", clientHandler.UpdateClientMTLS)
				clients.DELETE("/:clientId/mtls", clientHandler.DeleteClientMTLS)
				// Client-side attachment routes
				clients.GET("/:clientId/routes", clientAttachmentHandler.ListClientRoutes)
				clients.POST("/:clientId/routes/attach", clientAttachmentHandler.AttachFromClient)
			}

			// AI routes
			aiRoutes := protected.Group("/ai")
			{
				aiRoutes.GET("/status", aiHandler.GetStatus)
				aiRoutes.POST("/chat", aiHandler.Chat)
			}

			// Projects
			projects := protected.Group("/projects")
			{
				projects.GET("", projectHandler.List)
				projects.POST("", authMiddleware.RequireRole("owner"), projectHandler.Create)
				projects.GET("/:projectId", projectHandler.Get)
				projects.PATCH("/:projectId", projectHandler.Update)
				projects.DELETE("/:projectId", projectHandler.Delete)
				projects.POST("/:projectId/test-connection", projectHandler.TestConnection)
				projects.POST("/:projectId/metrics/test-connection", metricsHandler.TestConnection)
				projects.GET("/:projectId/capabilities", projectHandler.GetCapabilities)
				projects.GET("/:projectId/versions", projectVersionHandler.Get)
				projects.POST("/:projectId/versions/refresh", projectVersionHandler.Refresh)

				// Project admins (Owner only)
				projects.GET("/:projectId/admins", projectHandler.ListAdmins)
				projects.POST("/:projectId/admins", authMiddleware.RequireRole("owner"), projectHandler.AddAdmin)
				projects.DELETE("/:projectId/admins/:userId", authMiddleware.RequireRole("owner"), projectHandler.RemoveAdmin)

				// User permissions for a project
				projects.GET("/:projectId/permissions", permissionHandler.GetPermissions)

				// Project Team Assignments
				// List teams - any project member can view (needed for route creation)
				projects.GET("/:projectId/teams", permChecker.RequireProjectAccess(), teamHandler.ListProjectTeams)
				projects.GET("/:projectId/teams/:teamId", permChecker.RequireProjectAccess(), teamHandler.GetProjectTeamRole)
				// List only teams the current user is a member of (for route owner selection)
				projects.GET("/:projectId/my-teams", permChecker.RequireProjectAccess(), teamHandler.ListMyTeamsInProject)
				// List all unique members across project teams (for @mention autocomplete)
				projects.GET("/:projectId/members", permChecker.RequireProjectAccess(), teamHandler.ListProjectMembers)

				// Manage teams - Owner or Project Admin only
				projectTeams := projects.Group("/:projectId/teams")
				projectTeams.Use(permChecker.RequireTeamAccess())
				{
					projectTeams.POST("", teamHandler.AssignTeamToProject)
					projectTeams.PATCH("/:teamId", teamHandler.UpdateTeamPresets)
					projectTeams.DELETE("/:teamId", teamHandler.RemoveTeamFromProject)
				}

				// Permission Presets (Owner or Project Admin only)
				presets := projects.Group("/:projectId/presets")
				presets.Use(permChecker.RequireTeamAccess())
				{
					presets.GET("", presetHandler.List)
					presets.POST("", presetHandler.Create)
					presets.GET("/:presetId", presetHandler.Get)
					presets.PATCH("/:presetId", presetHandler.Update)
					presets.DELETE("/:presetId", presetHandler.Delete)
				}

				// Domain Templates - Read access (users who can manage domains can list/view templates)
				domainTemplatesRead := projects.Group("/:projectId/domain-templates")
				domainTemplatesRead.Use(permChecker.RequireDomainTemplateReadAccess())
				{
					domainTemplatesRead.GET("", domainTemplateHandler.List)
					domainTemplatesRead.GET("/:domainTemplateId", domainTemplateHandler.Get)
					domainTemplatesRead.GET("/:domainTemplateId/manifests", domainTemplateHandler.GetManifests)
					domainTemplatesRead.GET("/:domainTemplateId/domains", domainTemplateHandler.ListDomains)
				}

				// Domain Templates - Write access (Owner or Project Admin only)
				domainTemplatesWrite := projects.Group("/:projectId/domain-templates")
				domainTemplatesWrite.Use(permChecker.RequireDomainTemplateAccess())
				{
					domainTemplatesWrite.POST("", domainTemplateHandler.Create)
					domainTemplatesWrite.POST("/preview-create", domainTemplateHandler.PreviewCreate)
					domainTemplatesWrite.PATCH("/:domainTemplateId", domainTemplateHandler.Update)
					domainTemplatesWrite.DELETE("/:domainTemplateId", domainTemplateHandler.Delete)
					domainTemplatesWrite.POST("/:domainTemplateId/preview-changes", domainTemplateHandler.PreviewChanges)
				}

				// Project Namespaces - managed namespaces for cross-namespace routing
				projectNamespaces := projects.Group("/:projectId/namespaces")
				projectNamespaces.Use(permChecker.RequireProjectAccess())
				{
					projectNamespaces.GET("", projectNamespaceHandler.List)
					projectNamespaces.POST("", projectNamespaceHandler.Create) // Permission check in handler
					projectNamespaces.GET("/:namespaceId", projectNamespaceHandler.Get)
					projectNamespaces.PATCH("/:namespaceId", projectNamespaceHandler.Update)                                     // Permission check in handler
					projectNamespaces.DELETE("/:namespaceId", projectNamespaceHandler.Delete)                                    // Permission check in handler
					projectNamespaces.POST("/:namespaceId/ensure-reference-grant", projectNamespaceHandler.EnsureReferenceGrant) // Permission check in handler
				}

				// Domains (view: any team member, manage: Owner/Project Admin)
				domains := projects.Group("/:projectId/domains")
				domains.Use(permChecker.RequireProjectAccess())
				{
					domains.GET("", domainHandler.List)
					domains.GET("/tls-secrets", domainHandler.ListTLSSecrets)
					domains.GET("/available-namespaces", domainHandler.ListAvailableNamespaces)
					domains.POST("", domainHandler.Create) // Permission check in handler
					domains.GET("/:domainId", domainHandler.Get)
					domains.PATCH("/:domainId", domainHandler.Update)  // Permission check in handler
					domains.DELETE("/:domainId", domainHandler.Delete) // Permission check in handler
					domains.GET("/:domainId/settings", domainHandler.GetDomainSettings)
					domains.PUT("/:domainId/settings", domainHandler.UpdateDomainSettings) // Permission check in handler

					// mTLS CA management
					domains.POST("/:domainId/settings/mtls/ca", domainHandler.AddDomainMTLSCA)            // Permission check in handler
					domains.DELETE("/:domainId/settings/mtls/ca/:caId", domainHandler.RemoveDomainMTLSCA) // Permission check in handler
					domains.GET("/:domainId/yamls", domainHandler.GetYAMLs)
					domains.GET("/:domainId/metrics", metricsHandler.GetDomainMetrics)
					domains.GET("/:domainId/topology", topologyHandler.GetDomainTopology)
					domains.POST("/:domainId/import/openapi", openapiImportHandler.Import)
					domains.POST("/:domainId/settings/preview", domainHandler.PreviewSettingsChanges)
					domains.POST("/preview-create", domainHandler.PreviewCreate)

					// AI generation under projects/domains
					domainAI := domains.Group("/:domainId/ai")
					{
						domainAI.POST("/generate", aiHandler.Generate)
						domainAI.POST("/review", aiHandler.Review)
					}

					// Routes (view: any team member, create: Owner/Admin/Editor)
					routes := domains.Group("/:domainId/routes")
					{
						routes.GET("", routeHandler.List)
						routes.POST("", routeHandler.Create)                         // Permission check in handler
						routes.POST("/preview", routeHandler.PreviewCreate)          // Preview create - no auth needed (just generates YAML)
						routes.POST("/check-conflicts", routeHandler.CheckConflicts) // Check matcher conflicts - no permission needed
						routes.GET("/:routeId/metrics", metricsHandler.GetRouteMetrics)
						routes.GET("/:routeId", routeHandler.Get)
						routes.PUT("/:routeId", routeHandler.Update)    // Permission check in handler
						routes.DELETE("/:routeId", routeHandler.Delete) // Permission check in handler
						routes.GET("/:routeId/yaml", routeHandler.GetYAML)
						routes.GET("/:routeId/yamls", routeHandler.GetYAMLs)                // Get both HTTPRoute and SecurityPolicy YAML
						routes.POST("/:routeId/preview", routeHandler.PreviewUpdate)        // Preview update
						routes.GET("/:routeId/preview-delete", routeHandler.PreviewDelete)  // Preview delete
						routes.POST("/:routeId/deploy", routeHandler.Deploy)                // Deploy to K8s - permission check in handler
						routes.GET("/:routeId/effective-ips", routeHandler.GetEffectiveIPs) // Get effective IP allowlist from active client attachments

						// Route version history
						routes.GET("/:routeId/versions", routeVersionHandler.List)
						routes.GET("/:routeId/versions/:version", routeVersionHandler.Get)
						routes.POST("/:routeId/versions/:version/rollback", routeVersionHandler.Rollback)

						// Route-side client attachment endpoints
						routes.GET("/:routeId/clients", clientAttachmentHandler.ListRouteClients)
						routes.POST("/:routeId/clients/attach", clientAttachmentHandler.AttachFromRoute)
						routes.POST("/:routeId/clients/:attachmentId/detach", clientAttachmentHandler.RequestDetachFromRoute)
					}
				}

				// Approvals (list: Owner/Admin/Approver, approve/reject: Owner/Admin/Approver)
				approvals := projects.Group("/:projectId/approvals")
				approvals.Use(permChecker.RequireApprovalAccess())
				{
					approvals.GET("", approvalHandler.List)
					approvals.GET("/:approvalId", approvalHandler.Get)
					approvals.GET("/:approvalId/diff", approvalHandler.GetDiff)
					approvals.POST("/:approvalId/stages/:stageId/approve", approvalHandler.Approve)
					approvals.POST("/:approvalId/stages/:stageId/reject", approvalHandler.Reject)
					approvals.POST("/:approvalId/cancel", approvalHandler.Cancel)
					// Approval comments
					approvals.GET("/:approvalId/comments", commentHandler.List)
					approvals.POST("/:approvalId/comments", commentHandler.Create)
					// AI review trigger
					approvals.POST("/:approvalId/ai-review", aiHandler.ReviewApproval)
				}

				// Client Attachment Approvals (any project member can view; approve/reject permission checked in service layer)
				clientApprovals := projects.Group("/:projectId/client-approvals")
				clientApprovals.Use(permChecker.RequireProjectAccess())
				{
					clientApprovals.GET("", clientAttachmentHandler.ListClientApprovals)
					clientApprovals.GET("/:approvalId", clientAttachmentHandler.GetClientApproval)
					clientApprovals.POST("/:approvalId/stages/:stageId/approve", clientAttachmentHandler.ApproveStage)
					clientApprovals.POST("/:approvalId/stages/:stageId/reject", clientAttachmentHandler.RejectClientApproval)
				}

				// Approval Policies (admin only)
				approvalPolicies := projects.Group("/:projectId/approval-policies")
				approvalPolicies.Use(permChecker.RequireAdminAccess())
				{
					approvalPolicies.GET("", approvalPolicyHandler.List)
					approvalPolicies.GET("/:policyId", approvalPolicyHandler.Get)
					approvalPolicies.POST("", approvalPolicyHandler.Create)
					approvalPolicies.PUT("/:policyId", approvalPolicyHandler.Update)
					approvalPolicies.DELETE("/:policyId", approvalPolicyHandler.Delete)
				}

				// Kubernetes discovery (any project member)
				k8s := projects.Group("/:projectId/kubernetes")
				k8s.Use(permChecker.RequireProjectAccess())
				{
					k8s.GET("/namespaces", k8sHandler.ListNamespaces)
					k8s.GET("/namespaces/:namespace/services", k8sHandler.ListServices)
					k8s.GET("/gateway-classes", k8sHandler.ListGatewayClasses)
				}

				// Project-scoped route listing across all domains; supports backend service+namespace filter.
				projects.GET("/:projectId/routes", permChecker.RequireProjectAccess(), routeHandler.ListByProject)

				// Project-scoped topology aggregator (read-only).
				projects.GET("/:projectId/topology", permChecker.RequireProjectAccess(), topologyHandler.GetProjectTopology)

				// Audit logs
				projects.GET("/:projectId/audit", permChecker.RequireAuditAccess(), auditHandler.List)
				projects.GET("/:projectId/audit/export", permChecker.RequireAuditAccess(), auditHandler.Export)
				projects.DELETE("/:projectId/audit/cleanup", permChecker.RequireAdminAccess(), auditHandler.Cleanup)
			}
		}
	}

	// Start server
	go func() {
		log.Printf("Starting server on port %s", cfg.APIPort)
		if err := router.Run(":" + cfg.APIPort); err != nil {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")
}
