package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/server"
	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/service"
)

var version = "0.2.0"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	failSafe := flag.Bool("failsafe", false, "set configured owned fans to full speed and exit")
	flag.Parse()
	sysRoot := environment("HA500_TOOLBOX_SYS_ROOT", "/sys")
	settingsPath := environment("HA500_TOOLBOX_CONFIG", "/var/lib/ha500-toolbox/settings.json")
	if *failSafe {
		return service.ForceConfiguredFansFull(sysRoot, settingsPath)
	}
	socketPath := environment("SOCKET_PATH", "/run/ha500-toolbox/app.sock")
	logPath := environment("SERVICE_LOG_FILE", "/var/log/ha500-toolbox/toolbox.log")
	gatewayPrefix := environment("GATEWAY_PREFIX", "/app/ha500_toolbox")
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}
	if err := removeStaleSocket(socketPath); err != nil {
		return err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on Unix socket: %w", err)
	}
	defer listener.Close()
	defer os.Remove(socketPath)
	if err := os.Chmod(socketPath, 0o600); err != nil {
		return fmt.Errorf("secure Unix socket: %w", err)
	}
	runtime, err := service.New(service.Options{
		SysRoot: sysRoot, CPUInfoPath: environment("HA500_TOOLBOX_CPUINFO", "/proc/cpuinfo"),
		SettingsPath: settingsPath, LogPath: logPath, Version: version,
	})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go runtime.Run(ctx)
	httpServer := &http.Server{
		Handler:           server.NewHandler(gatewayPrefix, runtime),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	serveError := make(chan error, 1)
	go func() {
		err := httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveError <- err
		}
		close(serveError)
	}()
	select {
	case <-ctx.Done():
	case err := <-serveError:
		if err != nil {
			_ = runtime.Close()
			return err
		}
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := httpServer.Shutdown(shutdownContext)
	return errors.Join(shutdownErr, runtime.Close())
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to replace non-socket path %s", path)
	}
	if connection, dialErr := net.DialTimeout("unix", path, 250*time.Millisecond); dialErr == nil {
		_ = connection.Close()
		return errors.New("another HA500 toolbox service is already listening")
	}
	return os.Remove(path)
}
