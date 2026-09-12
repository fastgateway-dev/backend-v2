package main

import (
	_ "embed"
	"log"
	"os"
	"os/signal"
	"syscall"

	approvalpkg "github.com/fastgateway-dev/backend-v2/internal/approval"
	"github.com/fastgateway-dev/backend-v2/internal/cluster"
	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/database"
	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/fastgateway-dev/backend-v2/internal/services/clients"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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

	// Initialize services.
	//
	// Phase 2E: every dependency below arrives at construction. Task 7
	// removed the last three setters (SetKubernetesService) by replacing the
	// 58-method Kubernetes interface with the role interfaces in
	// internal/services/k8s_roles.go; each service is handed only the roles
	// it calls.
	systemSettingsService := services.NewSystemSettingsService(systemSettingsRepo, cfg)

	// ForceSSOPolicy is the force-SSO decision extracted out of SSOService so
	// that AuthService can depend on it without depending on SSOService --
	// SSOService needs AuthService as its TokenIssuer, which would otherwise
	// be a construction cycle. It needs only the SSO config repository, so it
	// can be built before either service and both dependencies stay required.
	ssoForcePolicy := services.NewForceSSOPolicy(ssoConfigRepo)
	authService := services.NewAuthService(services.AuthServiceDeps{
		UserRepo:     userRepo,
		APITokenRepo: apiTokenRepo,
		Config:       cfg,
		SSO:          ssoForcePolicy,
		Settings:     systemSettingsService,
	})
	ssoService := services.NewSSOService(services.SSOServiceDeps{
		SSOConfigRepo:   ssoConfigRepo,
		UserRepo:        userRepo,
		TeamRepo:        teamRepo,
		EmailInviteRepo: emailInviteRepo,
		Config:          cfg,
		Tokens:          authService,
		Settings:        systemSettingsService,
	})
	userService := services.NewUserService(userRepo)

	// Seed default admin user (hashes password at runtime)
	if err := userService.SeedDefaultAdmin(cfg.AdminUsername, cfg.AdminPassword, cfg.AdminEmail); err != nil {
		log.Fatalf("Failed to seed default admin user: %v", err)
	}
	// ProjectService and the cluster client need each other: the client reads
	// a project's connection details through cluster.ProjectCredentials, and
	// ProjectService validates a cluster's prerequisites through
	// services.Preflight. lazyProjectCredentials (bottom of this file) orders
	// the two constructions, the same way the RouteUpdaterFunc closure below
	// orders RouteService and RouteVersionService. cluster.Client only calls
	// its credential source per request, never at construction.
	var projectService *services.ProjectService
	k8sService := cluster.New(lazyProjectCredentials{
		get: func() cluster.ProjectCredentials { return projectService },
	})
	projectService = services.NewProjectService(services.ProjectServiceDeps{
		ProjectRepo:        projectRepo,
		ApprovalPolicyRepo: approvalPolicyRepo,
		PresetRepo:         presetRepo,
		Config:             cfg,
		K8sPreflight:       k8sService,
	})
	presetService := services.NewPresetService(presetRepo)
	teamService := services.NewTeamService(teamRepo, userRepo, presetRepo)
	aiService := services.NewAIService(cfg)
	domainTemplateService := services.NewDomainTemplateService(domainTemplateRepo, projectRepo, domainRepo, k8sService, aiService)
	domainService := services.NewDomainService(services.DomainServiceDeps{
		DomainRepo:           domainRepo,
		ProjectRepo:          projectRepo,
		DomainTemplateRepo:   domainTemplateRepo,
		K8sGateways:          k8sService,
		K8sSecrets:           k8sService,
		K8sBackends:          k8sService,
		K8sPolicies:          k8sService,
		K8sRefGrants:         k8sService,
		SettingsRepo:         domainSettingsRepo,
		ClientAttachmentRepo: clientAttachmentRepo,
		BtpRepo:              backendTrafficPolicyRepo,
		ExtPolicyRepo:        envoyExtensionPolicyRepo,
		ProjectNamespaceRepo: projectNamespaceRepo,
		DtService:            domainTemplateService,
		AiService:            aiService,
	})
	wafConfig := routeplan.WAFConfig{Image: cfg.WAFImage, Tag: cfg.WAFTag}

	// Approval engine: the single owner of stage planning and traversal for
	// every approvable entity type. Every dependency is required --
	// approvalpkg.New panics on a nil one rather than degrading silently.
	// It is built before its completers so each of them can take it as a
	// required constructor parameter; Register runs once they all exist.
	approvalEngine := approvalpkg.New(approvalRepo, approvalStageReviewRepo, approvalPolicyRepo, teamRepo, projectRepo)

	// RouteService and RouteVersionService need each other: a deploy records
	// a version snapshot, and a rollback resubmits a stored config through
	// RouteService.Update. Both dependencies are required constructor
	// parameters; the closure below is what orders the two constructions,
	// and routeService is assigned on the statement immediately after
	// NewRouteVersionService returns, long before any request can run it.
	var routeService *services.RouteService
	routeVersionService := services.NewRouteVersionService(services.RouteVersionServiceDeps{
		VersionRepo:              routeVersionRepo,
		RouteRepo:                routeRepo,
		SecurityPolicyRepo:       securityPolicyRepo,
		BackendTrafficPolicyRepo: backendTrafficPolicyRepo,
		EnvoyExtensionPolicyRepo: envoyExtensionPolicyRepo,
		WafPolicyRepo:            wafPolicyRepo,
		RouteUpdater: services.RouteUpdaterFunc(
			func(routeID uuid.UUID, input *services.UpdateRouteInput, submittedBy uuid.UUID) (*models.Route, error) {
				return routeService.Update(routeID, input, submittedBy)
			}),
	})
	routeService = services.NewRouteService(services.RouteServiceDeps{
		RouteRepo:                routeRepo,
		ApprovalRepo:             approvalRepo,
		PolicyRepo:               approvalPolicyRepo,
		DomainRepo:               domainRepo,
		TeamRepo:                 teamRepo,
		ProjectNamespaceRepo:     projectNamespaceRepo,
		SecurityPolicyRepo:       securityPolicyRepo,
		BackendTrafficPolicyRepo: backendTrafficPolicyRepo,
		EnvoyExtensionPolicyRepo: envoyExtensionPolicyRepo,
		WafPolicyRepo:            wafPolicyRepo,
		ClientAttachmentRepo:     clientAttachmentRepo,
		ClientIPRepo:             clientIPRepo,
		ClientHeaderRepo:         clientHeaderRepo,
		ClientRepo:               clientRepo,
		ProjectRepo:              projectRepo,
		WafConfig:                wafConfig,
		Domains:                  domainService,
		RouteVersions:            routeVersionService,
		Approvals:                approvalEngine,
		K8sRoutes:                k8sService,
		K8sPolicies:              k8sService,
		K8sBackends:              k8sService,
		K8sBackendReaper:         k8sService,
		K8sSecrets:               k8sService,
		K8sAPIKeys:               k8sService,
		K8sRefGrants:             k8sService,
	})
	approvalService := services.NewApprovalService(services.ApprovalServiceDeps{
		ApprovalRepo: approvalRepo,
		PolicyRepo:   approvalPolicyRepo,
		RouteRepo:    routeRepo,
		DomainRepo:   domainRepo,
		WafConfig:    wafConfig,
		Approvals:    approvalEngine,
	})
	auditService := services.NewAuditService(auditLogRepo)
	clientService := clients.NewClientService(clients.ClientServiceDeps{
		ClientRepo:           clientRepo,
		ClientIPRepo:         clientIPRepo,
		ClientHeaderRepo:     clientHeaderRepo,
		TeamRepo:             teamRepo,
		ClientAttachmentRepo: clientAttachmentRepo,
		RouteRepo:            routeRepo,
		K8sSecrets:           k8sService,
		K8sAPIKeys:           k8sService,
	})
	clientAttachmentService := clients.NewClientAttachmentService(clients.ClientAttachmentServiceDeps{
		AttachmentRepo:     clientAttachmentRepo,
		ApprovalRepo:       approvalRepo,
		ClientRepo:         clientRepo,
		RouteRepo:          routeRepo,
		DomainRepo:         domainRepo,
		ProjectRepo:        projectRepo,
		DomainSettingsRepo: domainSettingsRepo,
		Approvals:          approvalEngine,
	})

	// Registration genuinely happens after all the completers exist.
	approvalEngine.Register(models.ApprovalEntityRoute, routeService)
	approvalEngine.Register(models.ApprovalEntityClientAttachment, clientAttachmentService)

	projectNamespaceService := services.NewProjectNamespaceService(projectNamespaceRepo, projectRepo, domainRepo, k8sService, k8sService)
	projectVersionService := services.NewProjectVersionService(services.ProjectVersionServiceDeps{K8s: k8sService})

	// Initialize email invite service
	emailInviteService := services.NewTeamEmailInviteService(
		emailInviteRepo,
		userRepo,
		teamRepo,
	)

	// Initialize comment and notification services
	commentService := services.NewCommentService(commentRepo, notificationRepo, approvalRepo, teamRepo)
	notificationService := services.NewNotificationService(notificationRepo)

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
	teamHandler := handlers.NewTeamHandler(teamService, permChecker, auditService, emailInviteService)
	domainTemplateHandler := handlers.NewDomainTemplateHandler(domainTemplateService, auditService, domainTemplateService)
	domainHandler := handlers.NewDomainHandler(domainService, auditService, permChecker, domainService)
	routeHandler := handlers.NewRouteHandler(routeService, auditService, permChecker)
	routeVersionHandler := handlers.NewRouteVersionHandler(routeVersionService, auditService)
	approvalPolicyService := services.NewApprovalPolicyService(approvalPolicyRepo)
	approvalHandler := handlers.NewApprovalHandler(approvalService, auditService)
	approvalPolicyHandler := handlers.NewApprovalPolicyHandler(approvalPolicyService)
	auditHandler := handlers.NewAuditHandler(auditService)
	k8sHandler := handlers.NewKubernetesHandler(k8sService)
	clientHandler := handlers.NewClientHandler(clientService, auditService, permChecker)
	clientAttachmentHandler := handlers.NewClientAttachmentHandler(clientAttachmentService, clientService, auditService, routeService, permChecker)
	projectNamespaceHandler := handlers.NewProjectNamespaceHandler(projectNamespaceService, auditService, permChecker)
	projectVersionHandler := handlers.NewProjectVersionHandler(projectVersionService)
	presetHandler := handlers.NewPresetHandler(presetService, auditService)
	commentHandler := handlers.NewCommentHandler(commentService)
	notificationHandler := handlers.NewNotificationHandler(notificationService)

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
	router := setupRouter(RouterDeps{
		AuthMiddleware:          authMiddleware,
		PermChecker:             permChecker,
		AuthHandler:             authHandler,
		SSOHandler:              ssoHandler,
		DocsHandler:             docsHandler,
		UserHandler:             userHandler,
		SystemSettingsHandler:   systemSettingsHandler,
		TeamHandler:             teamHandler,
		ClientHandler:           clientHandler,
		ClientAttachmentHandler: clientAttachmentHandler,
		AIHandler:               aiHandler,
		ProjectHandler:          projectHandler,
		MetricsHandler:          metricsHandler,
		ProjectVersionHandler:   projectVersionHandler,
		PermissionHandler:       permissionHandler,
		PresetHandler:           presetHandler,
		DomainTemplateHandler:   domainTemplateHandler,
		ProjectNamespaceHandler: projectNamespaceHandler,
		DomainHandler:           domainHandler,
		TopologyHandler:         topologyHandler,
		OpenAPIImportHandler:    openapiImportHandler,
		RouteHandler:            routeHandler,
		RouteVersionHandler:     routeVersionHandler,
		ApprovalHandler:         approvalHandler,
		CommentHandler:          commentHandler,
		ApprovalPolicyHandler:   approvalPolicyHandler,
		K8sHandler:              k8sHandler,
		AuditHandler:            auditHandler,
		NotificationHandler:     notificationHandler,
	})

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

// RouterDeps bundles every handler, middleware instance, and permission
// checker that setupRouter wires onto the returned *gin.Engine. It exists so
// route registration -- previously inline in main(), built straight from the
// database-backed dependency graph -- is reachable from a test (see
// router_test.go's TestRouteSpecParity) without constructing a real
// database connection, repositories, or services.
type RouterDeps struct {
	AuthMiddleware *middleware.AuthMiddleware
	PermChecker    *middleware.PermissionChecker

	AuthHandler             *handlers.AuthHandler
	SSOHandler              *handlers.SSOHandler
	DocsHandler             *handlers.DocsHandler
	UserHandler             *handlers.UserHandler
	SystemSettingsHandler   *handlers.SystemSettingsHandler
	TeamHandler             *handlers.TeamHandler
	ClientHandler           *handlers.ClientHandler
	ClientAttachmentHandler *handlers.ClientAttachmentHandler
	AIHandler               *handlers.AIHandler
	ProjectHandler          *handlers.ProjectHandler
	MetricsHandler          *handlers.MetricsHandler
	ProjectVersionHandler   *handlers.ProjectVersionHandler
	PermissionHandler       *handlers.PermissionHandler
	PresetHandler           *handlers.PresetHandler
	DomainTemplateHandler   *handlers.DomainTemplateHandler
	ProjectNamespaceHandler *handlers.ProjectNamespaceHandler
	DomainHandler           *handlers.DomainHandler
	TopologyHandler         *handlers.TopologyHandler
	OpenAPIImportHandler    *handlers.OpenAPIImportHandler
	RouteHandler            *handlers.RouteHandler
	RouteVersionHandler     *handlers.RouteVersionHandler
	ApprovalHandler         *handlers.ApprovalHandler
	CommentHandler          *handlers.CommentHandler
	ApprovalPolicyHandler   *handlers.ApprovalPolicyHandler
	K8sHandler              *handlers.KubernetesHandler
	AuditHandler            *handlers.AuditHandler
	NotificationHandler     *handlers.NotificationHandler
}

// setupRouter registers every route on a fresh *gin.Engine. This is a pure
// move of the route registration that used to live inline in main() (Phase
// 2O Task 7) -- no route was added, removed, renamed, or given different
// middleware/handlers in the move. main() now just supplies a RouterDeps and
// calls this; TestRouteSpecParity calls it directly with test doubles to
// check the registered routes against cmd/server/openapi.yaml without
// standing up a database.
func setupRouter(deps RouterDeps) *gin.Engine {
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
			auth.POST("/login", deps.AuthHandler.Login)
			auth.POST("/refresh", deps.AuthHandler.RefreshToken)

			// SSO routes (public - no auth required)
			sso := auth.Group("/sso")
			{
				sso.GET("/config", deps.SSOHandler.GetPublicConfig)
				sso.GET("/authorize", deps.SSOHandler.Authorize)
				sso.GET("/callback", deps.SSOHandler.Callback)
			}
		}

		// Protected routes
		protected := v1.Group("")
		protected.Use(deps.AuthMiddleware.Authenticate())
		{
			// Auth routes (protected)
			protectedAuth := protected.Group("/auth")
			{
				protectedAuth.POST("/logout", deps.AuthHandler.Logout)
				protectedAuth.GET("/me", deps.AuthHandler.GetCurrentUser)
				protectedAuth.PUT("/password", deps.AuthHandler.ChangePassword)
				protectedAuth.GET("/tokens/capabilities", deps.AuthHandler.GetAPITokenCapabilities)
				protectedAuth.GET("/tokens", deps.AuthHandler.ListAPITokens)
				protectedAuth.POST("/tokens", deps.AuthHandler.CreateAPIToken)
				protectedAuth.DELETE("/tokens/:tokenId", deps.AuthHandler.RevokeAPIToken)
			}

			// Notifications (any authenticated user)
			notifications := protected.Group("/notifications")
			{
				notifications.GET("", deps.NotificationHandler.List)
				notifications.GET("/count", deps.NotificationHandler.CountUnread)
				notifications.PUT("/:notificationId/read", deps.NotificationHandler.MarkAsRead)
				notifications.PUT("/read-all", deps.NotificationHandler.MarkAllAsRead)
			}

			// API Documentation (any authenticated user)
			protected.GET("/docs/openapi.yaml", deps.DocsHandler.GetOpenAPISpec)

			// User management (Owner only)
			users := protected.Group("/users")
			users.Use(deps.AuthMiddleware.RequireRole("owner"))
			{
				users.GET("", deps.UserHandler.List)
				users.POST("", deps.UserHandler.Create)
				users.GET("/:userId", deps.UserHandler.Get)
				users.PATCH("/:userId", deps.UserHandler.Update)
				users.DELETE("/:userId", deps.UserHandler.Delete)
			}

			// SSO settings (Owner only)
			ssoSettings := protected.Group("/settings/sso")
			ssoSettings.Use(deps.AuthMiddleware.RequireRole("owner"))
			{
				ssoSettings.GET("", deps.SSOHandler.GetConfig)
				ssoSettings.PUT("", deps.SSOHandler.UpdateConfig)
				ssoSettings.DELETE("", deps.SSOHandler.DisableSSO)
			}

			// System settings (Owner only)
			systemSettings := protected.Group("/settings/system")
			systemSettings.Use(deps.AuthMiddleware.RequireRole("owner"))
			{
				systemSettings.GET("", deps.SystemSettingsHandler.Get)
				systemSettings.PUT("", deps.SystemSettingsHandler.Update)
			}

			// My Teams (any authenticated user)
			protected.GET("/my-teams", deps.TeamHandler.ListMyTeams)

			// Global Teams - list endpoint (Owner or users with project.teams permission)
			// This allows users who can assign teams to projects to see available teams
			protected.GET("/teams", deps.TeamHandler.List)

			// Global Teams - management endpoints (Owner only)
			teams := protected.Group("/teams")
			teams.Use(deps.AuthMiddleware.RequireRole("owner"))
			{
				teams.POST("", deps.TeamHandler.Create)
				teams.GET("/:teamId", deps.TeamHandler.Get)
				teams.PATCH("/:teamId", deps.TeamHandler.Update)
				teams.DELETE("/:teamId", deps.TeamHandler.Delete)
				teams.GET("/:teamId/members", deps.TeamHandler.ListMembers)
				teams.POST("/:teamId/members", deps.TeamHandler.AddMember)
				teams.DELETE("/:teamId/members/:userId", deps.TeamHandler.RemoveMember)
				teams.GET("/:teamId/projects", deps.TeamHandler.ListTeamProjects)
				// Team email invites
				teams.POST("/:teamId/members/email", deps.TeamHandler.AddMemberByEmail)
				teams.GET("/:teamId/invites", deps.TeamHandler.ListInvites)
				teams.DELETE("/:teamId/invites/:inviteId", deps.TeamHandler.DeleteInvite)
			}

			// Global Clients (authenticated users)
			clients := protected.Group("/clients")
			{
				clients.GET("", deps.ClientHandler.List)
				clients.POST("", deps.ClientHandler.Create)
				clients.GET("/:clientId", deps.ClientHandler.Get)
				clients.PATCH("/:clientId", deps.ClientHandler.Update)
				clients.DELETE("/:clientId", deps.ClientHandler.Delete)
				clients.GET("/:clientId/ips", deps.ClientHandler.ListIPs)
				clients.POST("/:clientId/ips", deps.ClientHandler.AddIP)
				clients.DELETE("/:clientId/ips/:ipId", deps.ClientHandler.RemoveIP)
				clients.GET("/:clientId/headers", deps.ClientHandler.ListHeaders)
				clients.POST("/:clientId/headers", deps.ClientHandler.AddHeader)
				clients.DELETE("/:clientId/headers/:headerId", deps.ClientHandler.RemoveHeader)
				// Method authorization routes
				clients.PUT("/:clientId/methods", deps.ClientHandler.SetAllowedMethods)
				// API Key routes
				clients.POST("/:clientId/api-key", deps.ClientHandler.GenerateAPIKey)
				clients.DELETE("/:clientId/api-key", deps.ClientHandler.RevokeAPIKey)
				// JWT routes
				clients.POST("/:clientId/jwt", deps.ClientHandler.ConfigureJWT)
				clients.PUT("/:clientId/jwt", deps.ClientHandler.UpdateJWT)
				clients.DELETE("/:clientId/jwt", deps.ClientHandler.RemoveJWT)
				// mTLS routes
				clients.PUT("/:clientId/mtls", deps.ClientHandler.UpdateClientMTLS)
				clients.DELETE("/:clientId/mtls", deps.ClientHandler.DeleteClientMTLS)
				// Client-side attachment routes
				clients.GET("/:clientId/routes", deps.ClientAttachmentHandler.ListClientRoutes)
				clients.POST("/:clientId/routes/attach", deps.ClientAttachmentHandler.AttachFromClient)
			}

			// AI routes
			aiRoutes := protected.Group("/ai")
			{
				aiRoutes.GET("/status", deps.AIHandler.GetStatus)
				aiRoutes.POST("/chat", deps.AIHandler.Chat)
			}

			// Projects
			projects := protected.Group("/projects")
			{
				projects.GET("", deps.ProjectHandler.List)
				projects.POST("", deps.AuthMiddleware.RequireRole("owner"), deps.ProjectHandler.Create)
				projects.GET("/:projectId", deps.ProjectHandler.Get)
				projects.PATCH("/:projectId", deps.ProjectHandler.Update)
				projects.DELETE("/:projectId", deps.ProjectHandler.Delete)
				projects.POST("/:projectId/test-connection", deps.ProjectHandler.TestConnection)
				projects.POST("/:projectId/metrics/test-connection", deps.MetricsHandler.TestConnection)
				projects.GET("/:projectId/capabilities", deps.ProjectHandler.GetCapabilities)
				projects.GET("/:projectId/versions", deps.ProjectVersionHandler.Get)
				projects.POST("/:projectId/versions/refresh", deps.ProjectVersionHandler.Refresh)

				// Project admins (Owner only)
				projects.GET("/:projectId/admins", deps.ProjectHandler.ListAdmins)
				projects.POST("/:projectId/admins", deps.AuthMiddleware.RequireRole("owner"), deps.ProjectHandler.AddAdmin)
				projects.DELETE("/:projectId/admins/:userId", deps.AuthMiddleware.RequireRole("owner"), deps.ProjectHandler.RemoveAdmin)

				// User permissions for a project
				projects.GET("/:projectId/permissions", deps.PermissionHandler.GetPermissions)

				// Project Team Assignments
				// List teams - any project member can view (needed for route creation)
				projects.GET("/:projectId/teams", deps.PermChecker.RequireProjectAccess(), deps.TeamHandler.ListProjectTeams)
				projects.GET("/:projectId/teams/:teamId", deps.PermChecker.RequireProjectAccess(), deps.TeamHandler.GetProjectTeamRole)
				// List only teams the current user is a member of (for route owner selection)
				projects.GET("/:projectId/my-teams", deps.PermChecker.RequireProjectAccess(), deps.TeamHandler.ListMyTeamsInProject)
				// List all unique members across project teams (for @mention autocomplete)
				projects.GET("/:projectId/members", deps.PermChecker.RequireProjectAccess(), deps.TeamHandler.ListProjectMembers)

				// Manage teams - Owner or Project Admin only
				projectTeams := projects.Group("/:projectId/teams")
				projectTeams.Use(deps.PermChecker.RequireTeamAccess())
				{
					projectTeams.POST("", deps.TeamHandler.AssignTeamToProject)
					projectTeams.PATCH("/:teamId", deps.TeamHandler.UpdateTeamPresets)
					projectTeams.DELETE("/:teamId", deps.TeamHandler.RemoveTeamFromProject)
				}

				// Permission Presets (Owner or Project Admin only)
				presets := projects.Group("/:projectId/presets")
				presets.Use(deps.PermChecker.RequireTeamAccess())
				{
					presets.GET("", deps.PresetHandler.List)
					presets.POST("", deps.PresetHandler.Create)
					presets.GET("/:presetId", deps.PresetHandler.Get)
					presets.PATCH("/:presetId", deps.PresetHandler.Update)
					presets.DELETE("/:presetId", deps.PresetHandler.Delete)
				}

				// Domain Templates - Read access (users who can manage domains can list/view templates)
				domainTemplatesRead := projects.Group("/:projectId/domain-templates")
				domainTemplatesRead.Use(deps.PermChecker.RequireDomainTemplateReadAccess())
				{
					domainTemplatesRead.GET("", deps.DomainTemplateHandler.List)
					domainTemplatesRead.GET("/:domainTemplateId", deps.DomainTemplateHandler.Get)
					domainTemplatesRead.GET("/:domainTemplateId/manifests", deps.DomainTemplateHandler.GetManifests)
					domainTemplatesRead.GET("/:domainTemplateId/domains", deps.DomainTemplateHandler.ListDomains)
				}

				// Domain Templates - Write access (Owner or Project Admin only)
				domainTemplatesWrite := projects.Group("/:projectId/domain-templates")
				domainTemplatesWrite.Use(deps.PermChecker.RequireDomainTemplateAccess())
				{
					domainTemplatesWrite.POST("", deps.DomainTemplateHandler.Create)
					domainTemplatesWrite.POST("/preview-create", deps.DomainTemplateHandler.PreviewCreate)
					domainTemplatesWrite.PATCH("/:domainTemplateId", deps.DomainTemplateHandler.Update)
					domainTemplatesWrite.DELETE("/:domainTemplateId", deps.DomainTemplateHandler.Delete)
					domainTemplatesWrite.POST("/:domainTemplateId/preview-changes", deps.DomainTemplateHandler.PreviewChanges)
				}

				// Project Namespaces - managed namespaces for cross-namespace routing
				projectNamespaces := projects.Group("/:projectId/namespaces")
				projectNamespaces.Use(deps.PermChecker.RequireProjectAccess())
				{
					projectNamespaces.GET("", deps.ProjectNamespaceHandler.List)
					projectNamespaces.POST("", deps.ProjectNamespaceHandler.Create) // Permission check in handler
					projectNamespaces.GET("/:namespaceId", deps.ProjectNamespaceHandler.Get)
					projectNamespaces.PATCH("/:namespaceId", deps.ProjectNamespaceHandler.Update)                                     // Permission check in handler
					projectNamespaces.DELETE("/:namespaceId", deps.ProjectNamespaceHandler.Delete)                                    // Permission check in handler
					projectNamespaces.POST("/:namespaceId/ensure-reference-grant", deps.ProjectNamespaceHandler.EnsureReferenceGrant) // Permission check in handler
				}

				// Domains (view: any team member, manage: Owner/Project Admin)
				domains := projects.Group("/:projectId/domains")
				domains.Use(deps.PermChecker.RequireProjectAccess())
				{
					domains.GET("", deps.DomainHandler.List)
					domains.GET("/tls-secrets", deps.DomainHandler.ListTLSSecrets)
					domains.GET("/available-namespaces", deps.DomainHandler.ListAvailableNamespaces)
					domains.POST("", deps.DomainHandler.Create) // Permission check in handler
					domains.GET("/:domainId", deps.DomainHandler.Get)
					domains.PATCH("/:domainId", deps.DomainHandler.Update)  // Permission check in handler
					domains.DELETE("/:domainId", deps.DomainHandler.Delete) // Permission check in handler
					domains.GET("/:domainId/settings", deps.DomainHandler.GetDomainSettings)
					domains.PUT("/:domainId/settings", deps.DomainHandler.UpdateDomainSettings) // Permission check in handler

					// mTLS CA management
					domains.POST("/:domainId/settings/mtls/ca", deps.DomainHandler.AddDomainMTLSCA)            // Permission check in handler
					domains.DELETE("/:domainId/settings/mtls/ca/:caId", deps.DomainHandler.RemoveDomainMTLSCA) // Permission check in handler
					domains.GET("/:domainId/yamls", deps.DomainHandler.GetYAMLs)
					domains.GET("/:domainId/metrics", deps.MetricsHandler.GetDomainMetrics)
					domains.GET("/:domainId/topology", deps.TopologyHandler.GetDomainTopology)
					domains.POST("/:domainId/import/openapi", deps.OpenAPIImportHandler.Import)
					domains.POST("/:domainId/settings/preview", deps.DomainHandler.PreviewSettingsChanges)
					domains.POST("/preview-create", deps.DomainHandler.PreviewCreate)

					// AI generation under projects/domains
					domainAI := domains.Group("/:domainId/ai")
					{
						domainAI.POST("/generate", deps.AIHandler.Generate)
						domainAI.POST("/review", deps.AIHandler.Review)
					}

					// Routes (view: any team member, create: Owner/Admin/Editor)
					routes := domains.Group("/:domainId/routes")
					{
						routes.GET("", deps.RouteHandler.List)
						routes.POST("", deps.RouteHandler.Create)                         // Permission check in handler
						routes.POST("/preview", deps.RouteHandler.PreviewCreate)          // Preview create - no auth needed (just generates YAML)
						routes.POST("/check-conflicts", deps.RouteHandler.CheckConflicts) // Check matcher conflicts - no permission needed
						routes.GET("/:routeId/metrics", deps.MetricsHandler.GetRouteMetrics)
						routes.GET("/:routeId", deps.RouteHandler.Get)
						routes.PUT("/:routeId", deps.RouteHandler.Update)    // Permission check in handler
						routes.DELETE("/:routeId", deps.RouteHandler.Delete) // Permission check in handler
						routes.GET("/:routeId/yaml", deps.RouteHandler.GetYAML)
						routes.GET("/:routeId/yamls", deps.RouteHandler.GetYAMLs)                // Get both HTTPRoute and SecurityPolicy YAML
						routes.POST("/:routeId/preview", deps.RouteHandler.PreviewUpdate)        // Preview update
						routes.GET("/:routeId/preview-delete", deps.RouteHandler.PreviewDelete)  // Preview delete
						routes.POST("/:routeId/deploy", deps.RouteHandler.Deploy)                // Deploy to K8s - permission check in handler
						routes.GET("/:routeId/effective-ips", deps.RouteHandler.GetEffectiveIPs) // Get effective IP allowlist from active client attachments

						// Route version history
						routes.GET("/:routeId/versions", deps.RouteVersionHandler.List)
						routes.GET("/:routeId/versions/:version", deps.RouteVersionHandler.Get)
						routes.POST("/:routeId/versions/:version/rollback", deps.RouteVersionHandler.Rollback)

						// Route-side client attachment endpoints
						routes.GET("/:routeId/clients", deps.ClientAttachmentHandler.ListRouteClients)
						routes.POST("/:routeId/clients/attach", deps.ClientAttachmentHandler.AttachFromRoute)
						routes.POST("/:routeId/clients/:attachmentId/detach", deps.ClientAttachmentHandler.RequestDetachFromRoute)
					}
				}

				// Approvals (list: Owner/Admin/Approver, approve/reject: Owner/Admin/Approver)
				approvals := projects.Group("/:projectId/approvals")
				approvals.Use(deps.PermChecker.RequireApprovalAccess())
				{
					approvals.GET("", deps.ApprovalHandler.List)
					approvals.GET("/:approvalId", deps.ApprovalHandler.Get)
					approvals.GET("/:approvalId/diff", deps.ApprovalHandler.GetDiff)
					approvals.POST("/:approvalId/stages/:stageId/approve", deps.ApprovalHandler.Approve)
					approvals.POST("/:approvalId/stages/:stageId/reject", deps.ApprovalHandler.Reject)
					approvals.POST("/:approvalId/cancel", deps.ApprovalHandler.Cancel)
					// Approval comments
					approvals.GET("/:approvalId/comments", deps.CommentHandler.List)
					approvals.POST("/:approvalId/comments", deps.CommentHandler.Create)
					// AI review trigger
					approvals.POST("/:approvalId/ai-review", deps.AIHandler.ReviewApproval)
				}

				// Client Attachment Approvals (any project member can view; approve/reject permission checked in service layer)
				clientApprovals := projects.Group("/:projectId/client-approvals")
				clientApprovals.Use(deps.PermChecker.RequireProjectAccess())
				{
					clientApprovals.GET("", deps.ClientAttachmentHandler.ListClientApprovals)
					clientApprovals.GET("/:approvalId", deps.ClientAttachmentHandler.GetClientApproval)
					clientApprovals.POST("/:approvalId/stages/:stageId/approve", deps.ClientAttachmentHandler.ApproveStage)
					clientApprovals.POST("/:approvalId/stages/:stageId/reject", deps.ClientAttachmentHandler.RejectClientApproval)
				}

				// Approval Policies (admin only)
				approvalPolicies := projects.Group("/:projectId/approval-policies")
				approvalPolicies.Use(deps.PermChecker.RequireAdminAccess())
				{
					approvalPolicies.GET("", deps.ApprovalPolicyHandler.List)
					approvalPolicies.GET("/:policyId", deps.ApprovalPolicyHandler.Get)
					approvalPolicies.POST("", deps.ApprovalPolicyHandler.Create)
					approvalPolicies.PUT("/:policyId", deps.ApprovalPolicyHandler.Update)
					approvalPolicies.DELETE("/:policyId", deps.ApprovalPolicyHandler.Delete)
				}

				// Kubernetes discovery (any project member)
				k8s := projects.Group("/:projectId/kubernetes")
				k8s.Use(deps.PermChecker.RequireProjectAccess())
				{
					k8s.GET("/namespaces", deps.K8sHandler.ListNamespaces)
					k8s.GET("/namespaces/:namespace/services", deps.K8sHandler.ListServices)
					k8s.GET("/gateway-classes", deps.K8sHandler.ListGatewayClasses)
				}

				// Project-scoped route listing across all domains; supports backend service+namespace filter.
				projects.GET("/:projectId/routes", deps.PermChecker.RequireProjectAccess(), deps.RouteHandler.ListByProject)

				// Project-scoped topology aggregator (read-only).
				projects.GET("/:projectId/topology", deps.PermChecker.RequireProjectAccess(), deps.TopologyHandler.GetProjectTopology)

				// Audit logs
				projects.GET("/:projectId/audit", deps.PermChecker.RequireAuditAccess(), deps.AuditHandler.List)
				projects.GET("/:projectId/audit/export", deps.PermChecker.RequireAuditAccess(), deps.AuditHandler.Export)
				projects.DELETE("/:projectId/audit/cleanup", deps.PermChecker.RequireAdminAccess(), deps.AuditHandler.Cleanup)
			}
		}
	}

	return router
}

// lazyProjectCredentials resolves the credential source on first use, which
// lets main() build *cluster.Client and *services.ProjectService in either
// order. It exists because the two genuinely need each other: the cluster
// client reads project connection details, and ProjectService validates a
// cluster's prerequisites. cluster.Client calls these three methods lazily,
// per request (internal/cluster/client.go), never at construction, so the
// pointer is always assigned by the time any of them runs.
//
// Phase 2E Task 7 introduced this to replace
// projectService.SetKubernetesService, the last of the three
// SetKubernetesService setters.
type lazyProjectCredentials struct {
	get func() cluster.ProjectCredentials
}

func (l lazyProjectCredentials) GetByID(id uuid.UUID) (*models.Project, error) {
	return l.get().GetByID(id)
}

func (l lazyProjectCredentials) GetDecryptedToken(id uuid.UUID) (string, error) {
	return l.get().GetDecryptedToken(id)
}

func (l lazyProjectCredentials) GetDecryptedClientKey(id uuid.UUID) (string, error) {
	return l.get().GetDecryptedClientKey(id)
}
