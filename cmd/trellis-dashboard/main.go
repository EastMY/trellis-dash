package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/yunnnn/trellis-dash/internal/api"
	"github.com/yunnnn/trellis-dash/internal/app"
	"github.com/yunnnn/trellis-dash/internal/config"
	"github.com/yunnnn/trellis-dash/internal/gitstate"
	"github.com/yunnnn/trellis-dash/internal/model"
	"github.com/yunnnn/trellis-dash/internal/store"
	"github.com/yunnnn/trellis-dash/internal/trellis"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "trellis-dashboard:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 && (args[0] == "version" || args[0] == "--version") {
		fmt.Printf("trellis-dashboard %s (%s)\n", version, commit)
		return nil
	}
	if len(args) > 0 && args[0] == "serve" {
		args = args[1:]
	}
	return serve(args)
}

type projectFlags []string

func (p *projectFlags) String() string { return strings.Join(*p, ",") }
func (p *projectFlags) Set(value string) error {
	*p = append(*p, value)
	return nil
}

func serve(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	var configPath, host, database, logLevel string
	var port int
	var projects projectFlags
	flags.StringVar(&configPath, "config", "", "YAML 配置文件路径")
	flags.StringVar(&host, "host", "", "监听地址，默认 127.0.0.1")
	flags.IntVar(&port, "port", 0, "监听端口，默认 7465")
	flags.StringVar(&database, "database", "", "SQLite 数据库路径")
	flags.Var(&projects, "project", "启动时注册的 Trellis 项目，可重复")
	flags.StringVar(&logLevel, "log-level", "info", "日志级别: debug/info/warn/error")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if host != "" {
		cfg.Server.Host = host
	}
	if port != 0 {
		cfg.Server.Port = port
	}
	if database != "" {
		cfg.Database.Path = database
	}
	logger := newLogger(logLevel)
	configured := append([]config.ProjectConfig{}, cfg.Projects...)
	for _, root := range projects {
		configured = append(configured, config.ProjectConfig{Root: root, Mode: model.ProjectModeObserver})
	}
	if err := validateDatabasePlacement(cfg.Database.Path, configured); err != nil {
		return err
	}

	repository, err := store.Open(cfg.Database.Path)
	if err != nil {
		return err
	}
	defer repository.Close()

	scanner := trellis.NewScanner()
	inspector := gitstate.NewInspector(cfg.Git.CommandTimeout.Duration, cfg.Git.MaxDiffBytes)
	supervisor := app.NewSupervisor(repository, scanner, inspector, logger, app.SupervisorOptions{
		Debounce:           cfg.Watcher.Debounce.Duration,
		RefreshInterval:    cfg.RefreshInterval.Duration,
		FullRescanInterval: cfg.Watcher.FullRescanInterval.Duration,
	})

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := supervisor.Start(rootCtx); err != nil {
		return fmt.Errorf("启动项目监督器: %w", err)
	}
	defer supervisor.Stop()

	for _, item := range configured {
		if err := registerProject(rootCtx, repository, supervisor, item); err != nil {
			logger.Warn("跳过无效项目", "root", item.Root, "error", err)
		}
	}

	handler := api.NewServer(repository, supervisor, inspector, logger)
	address := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	server := &http.Server{
		Addr: address, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("Trellis Dashboard 已启动", "url", "http://"+address)
		serverErr <- server.ListenAndServe()
	}()

	select {
	case <-rootCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-serverErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// validateDatabasePlacement 必须在 store.Open 之前调用，避免先写入后才发现违反 Observer 边界。
func validateDatabasePlacement(databasePath string, configured []config.ProjectConfig) error {
	if err := store.ValidateStoredProjectLocations(databasePath); err != nil {
		return err
	}
	for _, item := range configured {
		root, err := trellis.ValidateRoot(item.Root)
		if err != nil {
			continue // 与后续注册行为一致：无效项目会记录警告后跳过。
		}
		if err := store.ValidateDatabaseOutsideProject(databasePath, root); err != nil {
			return err
		}
	}
	return nil
}

func registerProject(ctx context.Context, repository *store.Store, supervisor *app.Supervisor, item config.ProjectConfig) error {
	root, err := trellis.ValidateRoot(item.Root)
	if err != nil {
		return err
	}
	if err := store.ValidateDatabaseOutsideProject(repository.DatabasePath(), root); err != nil {
		return err
	}
	if existing, err := repository.GetProjectByRoot(ctx, root); err == nil {
		// Supervisor.Start 已恢复数据库中的项目，避免重复注册造成一次无意义的取消日志。
		_ = existing
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	name := strings.TrimSpace(item.Name)
	if name == "" {
		name = filepath.Base(root)
	}
	id := strings.TrimSpace(item.ID)
	if id == "" {
		id = projectID(name, root)
	}
	mode := item.Mode
	if mode == "" {
		mode = model.ProjectModeObserver
	}
	if mode != model.ProjectModeObserver {
		return fmt.Errorf("首版仅支持 observer 模式")
	}
	project := model.Project{ID: id, Name: name, Root: root, Mode: mode}
	if err := repository.UpsertProject(ctx, project); err != nil {
		return err
	}
	project, err = repository.GetProject(ctx, id)
	if err != nil {
		return err
	}
	supervisor.Register(project)
	return nil
}

var slugPattern = regexp.MustCompile(`[^a-z0-9]+`)

func projectID(name, root string) string {
	base := strings.Trim(slugPattern.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if base == "" {
		base = "project"
	}
	hash := sha256.Sum256([]byte(root))
	return base + "-" + hex.EncodeToString(hash[:3])
}

func newLogger(level string) *slog.Logger {
	var parsed slog.Level
	switch strings.ToLower(level) {
	case "debug":
		parsed = slog.LevelDebug
	case "warn":
		parsed = slog.LevelWarn
	case "error":
		parsed = slog.LevelError
	default:
		parsed = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: parsed}))
}
