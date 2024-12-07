//go:debug http2server=0

package main

import (
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/streamer45/tunneler/pkg/service"
)

func main() {
	var sshAddr string
	var httpAddr string
	var httpsAddr string
	var tlsCertPath string
	var tlsKeyPath string
	var hostKeyPath string

	hostname, _ := os.Hostname()

	flag.StringVar(&sshAddr, "ssh-addr", ":2222", "address used by the SSH server to listen to")
	flag.StringVar(&httpAddr, "http-addr", "127.0.0.1:8080", "address used by the HTTP server to listen to")
	flag.StringVar(&httpsAddr, "https-addr", "127.0.0.1:8443", "address used by the HTTPs server to listen to")
	flag.StringVar(&hostname, "hostname", hostname, "public hostname used to reach this service")
	flag.StringVar(&tlsCertPath, "tls-cert-path", "", "path to a valid TLS certificate used by the HTTPs server")
	flag.StringVar(&tlsKeyPath, "tls-key-path", "", "path to a valid TLS key used by the HTTPs server")
	flag.StringVar(&hostKeyPath, "host-key-path", "", "path to a valid host key used by the SSH server")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	srv, err := service.New(logger, service.Config{
		SSHAddr:     sshAddr,
		HTTPAddr:    httpAddr,
		HTTPSAddr:   httpsAddr,
		Hostname:    hostname,
		TLSCertPath: tlsCertPath,
		TLSKeyPath:  tlsKeyPath,
		HostKeyPath: hostKeyPath,
	})
	if err != nil {
		logger.Error("failed to initialize service", slog.String("err", err.Error()))
		os.Exit(1)
	}

	if err := srv.Start(); err != nil {
		logger.Error("failed to start service", slog.String("err", err.Error()))
		os.Exit(1)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	<-sig

	logger.Info("shutting down")

	if err := srv.Stop(); err != nil {
		logger.Error("failed to stop service", slog.String("err", err.Error()))
		os.Exit(1)
	}
}
