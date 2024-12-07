package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gliderlabs/ssh"
	gossh "golang.org/x/crypto/ssh"
)

const (
	shutdownTimeout = 10 * time.Second
	minValidPortNum = 1
	maxValidPortNum = 65535
)

type tunnel struct {
	localAddr string
	conn      gossh.Conn
	scheme    string
}

type Service struct {
	log *slog.Logger
	cfg Config

	sshSrv   ssh.Server
	sshLn    net.Listener
	httpSrv  http.Server
	httpLn   net.Listener
	httpsSrv http.Server
	httpsLn  net.Listener

	tunnels map[string]*tunnel

	mut sync.Mutex
}

type Config struct {
	SSHAddr     string
	HTTPAddr    string
	HTTPSAddr   string
	Hostname    string
	TLSCertPath string
	TLSKeyPath  string
	HostKeyPath string
}

func (c Config) IsValid() error {
	if c.SSHAddr == "" {
		return fmt.Errorf("invalid empty SSHAddr")
	}

	if c.HTTPAddr == "" {
		return fmt.Errorf("invalid empty HTTPAddr")
	}

	if c.HTTPSAddr == "" {
		return fmt.Errorf("invalid empty HTTPSAddr")
	}

	if c.Hostname == "" {
		return fmt.Errorf("invalid empty Hostname")
	}

	if c.TLSCertPath == "" {
		return fmt.Errorf("invalid empty TLSCertPath")
	}

	if c.TLSKeyPath == "" {
		return fmt.Errorf("invalid empty TLSKeyPath")
	}

	if c.HostKeyPath == "" {
		return fmt.Errorf("invalid empty HostKeyPath")
	}

	return nil
}

func New(log *slog.Logger, cfg Config) (*Service, error) {
	if err := cfg.IsValid(); err != nil {
		return nil, err
	}

	return &Service{
		cfg:     cfg,
		log:     log,
		tunnels: make(map[string]*tunnel),
	}, nil
}

func (s *Service) Start() error {
	s.mut.Lock()
	defer s.mut.Unlock()

	s.log.Info("service starting")

	sshListener, err := net.Listen("tcp4", s.cfg.SSHAddr)
	if err != nil {
		return fmt.Errorf("failed to initialize ssh listener: %w", err)
	}
	s.sshLn = sshListener

	s.log.Info("SSH listener started", slog.String("addr", sshListener.Addr().String()))

	httpListener, err := net.Listen("tcp4", s.cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("failed to initialize http listener: %w", err)
	}
	s.httpLn = httpListener

	s.log.Info("HTTP listener started", slog.String("addr", httpListener.Addr().String()))

	httpsListener, err := net.Listen("tcp4", s.cfg.HTTPSAddr)
	if err != nil {
		return fmt.Errorf("failed to initialize https listener: %w", err)
	}
	s.httpsLn = httpsListener

	s.log.Info("HTTPs listener started", slog.String("addr", httpsListener.Addr().String()))

	s.sshSrv = ssh.Server{
		Addr: s.cfg.SSHAddr,
		Handler: ssh.Handler(func(s ssh.Session) {
			_, _ = io.WriteString(s, "Remote forwarding available...\n")
			select {}
		}),
		ReversePortForwardingCallback: ssh.ReversePortForwardingCallback(func(ctx ssh.Context, host string, port uint32) bool {
			log.Println("attempt to bind", host, port, "granted")
			return true
		}),
		RequestHandlers: map[string]ssh.RequestHandler{
			"tcpip-forward":        s.forwardHandler,
			"cancel-tcpip-forward": s.cancelForwardHandler,
		},
	}

	if err := s.sshSrv.SetOption(ssh.HostKeyFile(s.cfg.HostKeyPath)); err != nil {
		return fmt.Errorf("failed to set ssh server option: %w", err)
	}

	s.httpSrv = http.Server{
		Addr:    s.cfg.HTTPAddr,
		Handler: s.createAPIMux(),
	}

	s.httpsSrv = http.Server{
		Addr:    s.cfg.HTTPSAddr,
		Handler: s.createAPIMux(),
	}

	go func() {
		_ = s.sshSrv.Serve(sshListener)
		s.log.Info("ssh server done")
	}()

	go func() {
		if err := s.httpSrv.Serve(httpListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Error("http server failed", slog.String("err", err.Error()))
		}
		s.log.Info("http server done")
	}()

	go func() {
		if err := s.httpsSrv.ServeTLS(httpsListener, s.cfg.TLSCertPath, s.cfg.TLSKeyPath); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Error("https server failed", slog.String("err", err.Error()))
		}
		s.log.Info("https server done")
	}()

	s.log.Info("service started, ready to accept requests")

	return nil
}

func (s *Service) Stop() error {
	s.mut.Lock()
	defer s.mut.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := s.sshSrv.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown SSH server: %w", err)
	}

	if err := s.httpSrv.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown HTTP server: %w", err)
	}

	return nil
}
