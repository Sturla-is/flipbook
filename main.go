package main

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jonradoff/flipbook/internal/auth"
	"github.com/jonradoff/flipbook/internal/config"
	"github.com/jonradoff/flipbook/internal/converter"
	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/handlers"
	"github.com/jonradoff/flipbook/internal/mcp"
	"github.com/jonradoff/flipbook/internal/saas"
	"github.com/jonradoff/flipbook/internal/storage"
	"github.com/jonradoff/flipbook/internal/worker"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	// Handle CLI subcommands
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "set-password":
			runSetPassword()
			return
		case "backfill-gridfs":
			runBackfillGridFS()
			return
		case "mcp":
			mcp.Run()
			return
		case "help":
			printHelp()
			return
		default:
			fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
			printHelp()
			os.Exit(1)
		}
	}

	runServer()
}

func printHelp() {
	fmt.Println("Usage: flipbook [command]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  (no command)       Start the web server")
	fmt.Println("  set-password       Set the admin password")
	fmt.Println("  backfill-gridfs    Upload existing originals to GridFS backup")
	fmt.Println("  mcp                Start the MCP server (stdin/stdout)")
	fmt.Println("  help               Show this help message")
}

func runSetPassword() {
	cfg := config.Load()

	db, err := database.Open(context.Background(), cfg.MongoURI, cfg.MongoDB)
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer db.Close(context.Background())

	fmt.Print("Enter new admin password: ")
	var password string
	fmt.Scanln(&password)

	if len(password) < 8 {
		fmt.Println("Error: Password must be at least 8 characters.")
		os.Exit(1)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		log.Fatalf("Failed to hash password: %v", err)
	}

	if err := db.SetSetting("admin_password_hash", string(hash)); err != nil {
		log.Fatalf("Failed to save password: %v", err)
	}

	fmt.Println("Admin password set successfully.")
}

func runBackfillGridFS() {
	cfg := config.Load()

	db, err := database.Open(context.Background(), cfg.MongoURI, cfg.MongoDB)
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer db.Close(context.Background())

	store := storage.New(filepath.Join(cfg.DataDir, "flipbooks"))

	flipbooks, err := db.ListFlipbooks()
	if err != nil {
		log.Fatalf("Failed to list flipbooks: %v", err)
	}

	for _, fb := range flipbooks {
		if fb.GridFSFileID != "" {
			fmt.Printf("  SKIP %s (%s) — already backed up\n", fb.ID, fb.Title)
			continue
		}

		ext := filepath.Ext(fb.Filename)
		srcPath := store.OriginalPath(fb.ID, ext)

		f, err := os.Open(srcPath)
		if err != nil {
			fmt.Printf("  MISS %s (%s) — original not found: %s\n", fb.ID, fb.Title, srcPath)
			continue
		}

		gridfsID, err := db.UploadToGridFS(context.Background(), fb.Filename, f)
		f.Close()
		if err != nil {
			fmt.Printf("  FAIL %s (%s) — GridFS upload: %v\n", fb.ID, fb.Title, err)
			continue
		}

		if err := db.SetGridFSFileID(fb.ID, gridfsID); err != nil {
			fmt.Printf("  FAIL %s (%s) — save ID: %v\n", fb.ID, fb.Title, err)
			continue
		}

		fmt.Printf("  OK   %s (%s) — backed up as %s\n", fb.ID, fb.Title, gridfsID)
	}

	fmt.Println("Backfill complete.")
}

func runServer() {
	cfg := config.Load()

	// Initialize database
	os.MkdirAll(cfg.DataDir, 0755)
	db, err := database.Open(context.Background(), cfg.MongoURI, cfg.MongoDB)
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer db.Close(context.Background())

	// Initialize storage
	store := storage.New(filepath.Join(cfg.DataDir, "flipbooks"))

	// Initialize converter
	conv := converter.New(cfg.LibreOfficeBin, filepath.Join(cfg.DataDir, "tmp"), cfg.ConversionDPI, cfg.ThumbnailDPI)

	// Initialize worker
	w := worker.New(db, store, conv)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// Re-queue stuck conversions
	stuck, _ := db.GetFlipbooksByStatus("converting")
	for _, fb := range stuck {
		log.Printf("Re-queuing stuck conversion: %s", fb.ID)
		db.UpdateStatus(fb.ID, "pending", "")
		ext := filepath.Ext(fb.Filename)
		w.Enqueue(worker.Job{FlipbookID: fb.ID, SourcePath: store.OriginalPath(fb.ID, ext)})
	}

	// Re-queue stuck regenerations
	regen, _ := db.GetFlipbooksByStatus("regenerating")
	for _, fb := range regen {
		log.Printf("Re-queuing stuck regeneration: %s", fb.ID)
		db.UpdateStatus(fb.ID, "pending", "")
		ext := filepath.Ext(fb.Filename)
		w.Enqueue(worker.Job{FlipbookID: fb.ID, SourcePath: store.OriginalPath(fb.ID, ext)})
	}

	// Integrity check: detect ready flipbooks with missing page files and restore from GridFS
	go func() {
		readyFlipbooks, err := db.GetFlipbooksByStatus("ready")
		if err != nil {
			log.Printf("Integrity check: failed to query flipbooks: %v", err)
			return
		}

		for _, fb := range readyFlipbooks {
			if store.HasPages(fb.ID) {
				continue
			}
			if fb.GridFSFileID == "" {
				log.Printf("Integrity check: %s (%s) missing pages but no GridFS backup, skipping", fb.ID, fb.Title)
				continue
			}

			log.Printf("Integrity check: %s (%s) missing pages, restoring from GridFS", fb.ID, fb.Title)

			ext := filepath.Ext(fb.Filename)
			dstPath := store.OriginalPath(fb.ID, ext)

			if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
				log.Printf("Integrity check: failed to create dir for %s: %v", fb.ID, err)
				continue
			}

			f, err := os.Create(dstPath)
			if err != nil {
				log.Printf("Integrity check: failed to create file for %s: %v", fb.ID, err)
				continue
			}

			_, err = db.DownloadFromGridFS(context.Background(), fb.GridFSFileID, f)
			f.Close()
			if err != nil {
				log.Printf("Integrity check: GridFS download failed for %s: %v", fb.ID, err)
				os.Remove(dstPath)
				db.UpdateStatus(fb.ID, "error", "Failed to restore from backup: "+err.Error())
				continue
			}

			db.UpdateStatus(fb.ID, "regenerating", "")
			w.Enqueue(worker.Job{FlipbookID: fb.ID, SourcePath: dstPath})
		}

		log.Println("Integrity check complete")
	}()

	// Parse templates
	tmpl := parseTemplates()

	// Setup handlers common to both modes
	viewerH := handlers.NewViewerHandler(db, store, tmpl, cfg.BaseURL)
	embedH := handlers.NewEmbedHandler(db, store, tmpl, cfg.BaseURL)
	apiH := handlers.NewAPIHandler(db, store, w, cfg.BaseURL, cfg.MaxUploadSize)

	// Setup router
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))
	r.Use(securityHeaders)

	// Static files
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	// Serve flipbook images with caching (path-traversal safe via filepath.Base)
	r.Get("/data/flipbooks/{id}/pages/{filename}", func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		id := filepath.Base(chi.URLParam(req, "id"))
		fname := filepath.Base(chi.URLParam(req, "filename"))
		http.ServeFile(rw, req, filepath.Join(cfg.DataDir, "flipbooks", id, "pages", fname))
	})
	r.Get("/data/flipbooks/{id}/thumbs/{filename}", func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		id := filepath.Base(chi.URLParam(req, "id"))
		fname := filepath.Base(chi.URLParam(req, "filename"))
		http.ServeFile(rw, req, filepath.Join(cfg.DataDir, "flipbooks", id, "thumbs", fname))
	})

	// Readiness probe: verifies a MongoDB primary is reachable so Fly's health
	// check can recycle a machine whose driver has wedged (unlike /health, which
	// is a static liveness check). Fails fast (3s) rather than the 30s default.
	r.Get("/readyz", func(rw http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			http.Error(rw, "db unavailable", http.StatusServiceUnavailable)
			return
		}
		rw.WriteHeader(http.StatusOK)
		rw.Write([]byte("ready"))
	})

	// Status endpoint (always accessible for upload progress tracking)
	r.Get("/api/flipbooks/{id}/status", apiH.FlipbookStatus)

	// Public viewers
	r.Get("/v/{slug}", viewerH.View)
	r.Get("/embed/{slug}", embedH.Embed)

	if cfg.SaaS.Enabled {
		setupSaaSMode(r, cfg, db, store, w, tmpl, viewerH, embedH)
	} else {
		setupAdminMode(r, cfg, db, store, w, tmpl, apiH)
	}

	// Start server
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       120 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1MB
	}

	mode := "admin-only"
	if cfg.SaaS.Enabled {
		mode = "SaaS multi-tenant"
	}
	log.Printf("Flipbook server starting on :%s (%s mode)", cfg.Port, mode)
	log.Printf("Base URL: %s", cfg.BaseURL)
	log.Printf("LibreOffice: %s", cfg.LibreOfficeBin)

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		cancel()
		srv.Shutdown(context.Background())
	}()

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// setupAdminMode configures routes for the original single-tenant admin mode.
func setupAdminMode(r chi.Router, cfg *config.Config, db *database.DB, store *storage.Storage, w *worker.Worker, tmpl *template.Template, apiH *handlers.APIHandler) {
	// Initialize auth
	a := auth.New(db, cfg.SessionSecret, tmpl)

	if !a.HasPassword() {
		log.Println("WARNING: No admin password set. Run './flipbook set-password' to secure the admin area.")
	}

	adminH := handlers.NewAdminHandler(db, store, w, tmpl, cfg.BaseURL, cfg.MaxUploadSize)

	// Auth routes (public)
	r.Get("/login", a.LoginPage)
	r.Post("/login", a.LoginSubmit)
	r.Post("/logout", a.LogoutHandler)

	// Admin routes (protected)
	r.Group(func(r chi.Router) {
		r.Use(a.RequireAuth)
		r.Get("/admin", adminH.Index)
		r.Get("/admin/upload", adminH.UploadForm)
		r.Post("/admin/upload", adminH.Upload)
		r.Post("/admin/import", adminH.ImportURL)
		r.Get("/admin/flipbooks/{id}", adminH.Detail)
		r.Post("/admin/flipbooks/{id}/delete", adminH.Delete)
		r.Post("/admin/flipbooks/{id}/settings", adminH.Settings)
	})

	// API routes (protected by API key)
	r.Group(func(r chi.Router) {
		r.Use(apiAuth(cfg.APIKey))
		r.Get("/api/flipbooks", apiH.ListFlipbooks)
		r.Post("/api/flipbooks", apiH.UploadFlipbook)
		r.Post("/api/flipbooks/import", apiH.ImportURL)
		r.Get("/api/flipbooks/{id}", apiH.GetFlipbook)
		r.Delete("/api/flipbooks/{id}", apiH.DeleteFlipbook)
	})

	// Root redirect
	r.Get("/", func(rw http.ResponseWriter, req *http.Request) {
		http.Redirect(rw, req, "/admin", http.StatusFound)
	})

	log.Printf("Admin UI:  %s/admin", cfg.BaseURL)
	log.Printf("API key:   %s", cfg.APIKey)
}

// setupSaaSMode configures routes for multi-tenant SaaS mode with LastSaaS integration.
func setupSaaSMode(r chi.Router, cfg *config.Config, db *database.DB, store *storage.Storage, w *worker.Worker, tmpl *template.Template, viewerH *handlers.ViewerHandler, embedH *handlers.EmbedHandler) {
	if cfg.SaaS.JWTSecret == "" {
		log.Fatal("SaaS mode requires FLIPBOOK_JWT_ACCESS_SECRET to be set")
	}

	// Initialize SaaS services
	mongoDB := db.Database()
	jwtValidator := saas.NewJWTValidator(cfg.SaaS.JWTSecret)
	saasMiddleware := saas.NewMiddleware(jwtValidator, mongoDB)
	billingChecker := saas.NewBillingChecker(mongoDB, cfg.SaaS.GracePeriodDays)

	// Enable billing checks on viewers
	viewerH.SetBilling(billingChecker)
	embedH.SetBilling(billingChecker)

	// SaaS API handler
	saasAPI := handlers.NewSaaSAPIHandler(db, store, w, billingChecker, cfg.BaseURL, cfg.MaxUploadSize)

	// Tenant-scoped API routes
	r.Route("/api/saas", func(r chi.Router) {
		r.Use(saasMiddleware.RequireJWT)
		r.Use(saasMiddleware.RequireTenant)
		r.Get("/flipbooks", saasAPI.ListFlipbooks)
		r.Post("/flipbooks", saasAPI.Upload)
		r.Get("/flipbooks/{id}", saasAPI.GetFlipbook)
		r.Put("/flipbooks/{id}", saasAPI.UpdateFlipbook)
		r.Delete("/flipbooks/{id}", saasAPI.DeleteFlipbook)
		r.Get("/usage", saasAPI.Usage)
	})

	// Admin API routes (protected by API key, for MCP / admin tooling)
	// These create legacy flipbooks with no tenant association.
	adminAPIH := handlers.NewAPIHandler(db, store, w, cfg.BaseURL, cfg.MaxUploadSize)
	r.Group(func(r chi.Router) {
		r.Use(apiAuth(cfg.APIKey))
		r.Get("/api/flipbooks", adminAPIH.ListFlipbooks)
		r.Post("/api/flipbooks", adminAPIH.UploadFlipbook)
		r.Post("/api/flipbooks/import", adminAPIH.ImportURL)
		r.Get("/api/flipbooks/{id}", adminAPIH.GetFlipbook)
		r.Delete("/api/flipbooks/{id}", adminAPIH.DeleteFlipbook)
	})

	// Serve SPA frontend if configured (for auth/billing/dashboard pages)
	if cfg.SaaS.FrontendDir != "" {
		log.Printf("Serving SaaS frontend from %s", cfg.SaaS.FrontendDir)
		spa := spaHandler{staticPath: cfg.SaaS.FrontendDir, indexPath: "index.html", appName: os.Getenv("APP_NAME")}
		// Mount SPA for all frontend routes (auth, dashboard, etc.)
		// These must come after API routes and specific handlers
		spaRoutes := []string{
			"/",
			"/setup",
			"/login", "/signup", "/verify-email", "/forgot-password", "/reset-password",
			"/auth/callback", "/dashboard", "/team", "/plan", "/buy-credits",
			"/billing/*", "/settings", "/messages", "/test-entitlements",
			"/flipbooks", "/flipbooks/*",
			"/last", "/last/*",
			"/p/*",
		}
		for _, route := range spaRoutes {
			r.Handle(route, spa)
		}
		// Static assets from the SPA build (JS, CSS, etc.)
		r.Handle("/assets/*", http.StripPrefix("", http.FileServer(http.Dir(cfg.SaaS.FrontendDir))))
	} else {
		// Fallback landing page when no SPA frontend is configured
		r.Get("/", func(rw http.ResponseWriter, req *http.Request) {
			tmpl.ExecuteTemplate(rw, "landing", map[string]interface{}{
				"Year": time.Now().Year(),
			})
		})
	}

	log.Printf("SaaS mode enabled (grace period: %d days)", cfg.SaaS.GracePeriodDays)
	log.Printf("API key (admin): %s", cfg.APIKey)
}

// spaHandler serves a single-page application. Existing files are served
// directly; all other paths get index.html so the SPA router handles them.
// When serving index.html it substitutes the {{APP_NAME}} placeholder so the
// browser tab shows the real brand immediately (before JS loads branding),
// rather than a literal "{{APP_NAME}}".
type spaHandler struct {
	staticPath string
	indexPath  string
	appName    string
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(h.staticPath, filepath.Clean(r.URL.Path))
	fi, err := os.Stat(path)
	if os.IsNotExist(err) || (err == nil && fi.IsDir()) {
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		h.serveIndex(w)
		return
	}
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	http.FileServer(http.Dir(h.staticPath)).ServeHTTP(w, r)
}

// serveIndex reads index.html and replaces the {{APP_NAME}} placeholder.
func (h spaHandler) serveIndex(w http.ResponseWriter) {
	data, err := os.ReadFile(filepath.Join(h.staticPath, h.indexPath))
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	appName := h.appName
	if appName == "" {
		appName = "App"
	}
	html := strings.Replace(string(data), "{{APP_NAME}}", appName, 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}

// securityHeaders adds standard security headers to all responses.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

// apiAuth middleware protects API routes with a bearer token.
func apiAuth(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := r.Header.Get("Authorization")
			if token == "Bearer "+apiKey {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized"}`))
		})
	}
}

func parseTemplates() *template.Template {
	funcMap := template.FuncMap{
		"last": func(i, total int) bool {
			return i == total-1
		},
		"add": func(a, b int) int {
			return a + b
		},
	}

	// Gather all template files
	var files []string
	patterns := []string{
		"web/templates/*.html",
		"web/templates/admin/*.html",
	}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			log.Printf("Warning: glob pattern %s: %v", pattern, err)
			continue
		}
		files = append(files, matches...)
	}

	if len(files) == 0 {
		log.Fatal("No template files found")
	}

	tmpl := template.Must(template.New("").Funcs(funcMap).ParseFiles(files...))
	return tmpl
}
