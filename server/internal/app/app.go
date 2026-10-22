package app

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/shangyue/font-label/server/internal/platform/auth"
	"github.com/shangyue/font-label/server/internal/platform/config"
	"github.com/shangyue/font-label/server/internal/platform/db"
	"github.com/shangyue/font-label/server/internal/platform/httpx"
	"github.com/shangyue/font-label/server/internal/platform/storage"
)

// Server wires configuration, database, storage and HTTP handlers.
type Server struct {
	cfg   config.Config
	db    *db.DB
	store *storage.Store
	login *loginLimiter
}

func New(cfg config.Config, database *db.DB, store *storage.Store) *Server {
	return &Server{cfg: cfg, db: database, store: store, login: newLoginLimiter()}
}

func (s *Server) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(httpx.RequestID(), httpx.Recover(), httpx.AccessLog(), httpx.CORS())
	r.GET("/healthz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := s.db.Pool.Ping(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "db": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := r.Group("/api/v1")

	// public
	api.POST("/auth/login", s.handleLogin)
	api.GET("/media/:id/:variant", s.handleMedia)

	// authenticated
	authed := api.Group("", auth.Middleware(s.cfg.JWTSecret, s.loadPrincipal))
	authed.GET("/auth/me", s.handleMe)
	authed.POST("/auth/password", s.handleChangePassword)
	authed.POST("/auth/logout", s.handleLogout)

	// admin: users / rubrics / settings / audit
	admin := authed.Group("", auth.Require("admin"))
	admin.GET("/users", s.handleListUsers)
	admin.POST("/users", s.handleCreateUser)
	admin.PATCH("/users/:id", s.handleUpdateUser)
	admin.POST("/users/:id/reset-password", s.handleResetPassword)
	admin.GET("/users/:id/stats", s.handleUserStats)
	admin.POST("/upload", s.handleUpload)
	admin.DELETE("/images/:id", s.handleDeleteImage)
	admin.POST("/images/:id/reassign", s.handleReassign)
	admin.GET("/rubrics", s.handleListRubrics)
	admin.POST("/rubrics", s.handleCreateRubric)
	admin.POST("/rubrics/:id/activate", s.handleActivateRubric)
	admin.GET("/settings", s.handleGetSettings)
	admin.PUT("/settings/:key", s.handlePutSetting)
	admin.GET("/audit-logs", s.handleAuditLogs)
	admin.POST("/exports", s.handleCreateExport)

	// folders (admin + reviewer read; admin writes)
	authed.GET("/folders", auth.Require("admin", "reviewer"), s.handleListFolders)
	admin.POST("/folders", s.handleCreateFolder)
	admin.PATCH("/folders/:id", s.handleUpdateFolder)
	admin.DELETE("/folders/:id", s.handleDeleteFolder)

	// annotator side
	work := authed.Group("", auth.Require("annotator", "admin"))
	work.GET("/claim-folders", s.handleClaimableFolders)
	work.POST("/claims", s.handleClaim)
	work.POST("/claims/release", s.handleRelease)
	work.GET("/claims/my", s.handleMyTasks)
	work.PUT("/images/:id/draft", s.handleSaveDraft)
	work.GET("/images/:id/draft", s.handleGetDraft)
	work.POST("/images/:id/submit", s.handleSubmit)

	// review
	rev := authed.Group("", auth.Require("reviewer", "admin"))
	rev.GET("/reviews/queue", s.handleReviewQueue)
	rev.GET("/reviews/mine", s.handleMyReviews)
	rev.GET("/reviews/reasons", s.handleReviewReasons)
	rev.POST("/images/:id/review", s.handleReview)

	// shared reads
	authed.GET("/images", s.handleListImages)
	authed.GET("/images/:id", s.handleGetImage)
	authed.GET("/images/:id/history", s.handleHistory)
	authed.GET("/rubric", s.handleActiveRubric)

	// stats
	authed.GET("/stats/overview", auth.Require("admin", "reviewer"), s.handleStatsOverview)
	authed.GET("/stats/annotators", auth.Require("admin", "reviewer"), s.handleStatsAnnotators)
	authed.GET("/stats/folders", auth.Require("admin", "reviewer"), s.handleStatsFolders)
	authed.GET("/stats/rejections", auth.Require("admin", "reviewer"), s.handleStatsRejections)
	authed.GET("/stats/timeseries", auth.Require("admin", "reviewer"), s.handleStatsTimeseries)
	authed.GET("/stats/me", s.handleStatsMe)

	// exports (download by owner or admin)
	authed.GET("/exports", s.handleListExports)
	authed.GET("/exports/:id/download", s.handleDownloadExport)

	return r
}

// loadPrincipal is the auth middleware loader: reads the live row so that
// disabling a user or bumping token_version invalidates tokens at once.
func (s *Server) loadPrincipal(ctx context.Context, id int64) (auth.Principal, int, string, error) {
	var p auth.Principal
	var tv int
	var status string
	err := s.db.Pool.QueryRow(ctx,
		`SELECT id, username, display_name, role, status, token_version FROM users WHERE id=$1`, id).
		Scan(&p.ID, &p.Username, &p.DisplayName, &p.Role, &status, &tv)
	if err != nil {
		return p, 0, "", err
	}
	return p, tv, status, nil
}

// EnsureSeedAdmin creates the first administrator when the users table is empty.
func (s *Server) EnsureSeedAdmin(ctx context.Context) error {
	var n int
	if err := s.db.Pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	hash, err := auth.HashPassword(s.cfg.AdminPassword)
	if err != nil {
		return err
	}
	_, err = s.db.Pool.Exec(ctx,
		`INSERT INTO users (username, password_hash, display_name, role, must_change_pwd)
		 VALUES ($1,$2,'系统管理员','admin',true)`, s.cfg.AdminUser, hash)
	if err != nil {
		return err
	}
	slog.Warn("seeded initial admin", "username", s.cfg.AdminUser)
	return nil
}

func (s *Server) pool() *pgxpool.Pool { return s.db.Pool }
