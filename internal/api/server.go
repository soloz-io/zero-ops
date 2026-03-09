package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/soloz-io/zero-ops/internal/api/handlers"
	"github.com/soloz-io/zero-ops/internal/api/middleware"
	"github.com/soloz-io/zero-ops/internal/config"
	"github.com/soloz-io/zero-ops/internal/db"
	"github.com/soloz-io/zero-ops/internal/service"
	customValidator "github.com/soloz-io/zero-ops/pkg/validator"
	"go.uber.org/zap"
)

type Server struct {
	router *gin.Engine
	server *http.Server
	config *config.Config
}

func NewServer(cfg *config.Config, dbPool *pgxpool.Pool, logger *zap.Logger) *Server {
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()

	if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
		customValidator.RegisterAll(v)
	}

	router.Use(middleware.RecoveryMiddleware())
	router.Use(middleware.LoggerMiddleware(logger))
	router.Use(middleware.MetricsMiddleware())
	router.Use(middleware.ErrorMiddleware())

	queries := db.New(dbPool)
	tenantService := service.NewTenantService(queries, logger)
	tenantHandler := handlers.NewTenantHandler(tenantService, queries)
	healthHandler := handlers.NewHealthHandler(dbPool)

	router.GET("/healthz", healthHandler.Healthz)
	router.GET("/readyz", healthHandler.Readyz)
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))

	v1 := router.Group("/api/v1")
	{
		v1.POST("/tenants", tenantHandler.Create)
		v1.GET("/tenants/:id", tenantHandler.Get)
		v1.GET("/tenants", tenantHandler.List)
		v1.PATCH("/tenants/:id", tenantHandler.Update)
		v1.DELETE("/tenants/:id", tenantHandler.Delete)
	}

	return &Server{
		router: router,
		config: cfg,
		server: &http.Server{
			Addr:    ":" + cfg.ServerPort,
			Handler: router,
		},
	}
}

func (s *Server) Router() *gin.Engine {
	return s.router
}

func (s *Server) Start() error {
	return s.server.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}
