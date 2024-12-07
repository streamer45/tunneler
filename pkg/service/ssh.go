package service

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/gliderlabs/ssh"
	gossh "golang.org/x/crypto/ssh"
)

type forwardReqPayload struct {
	BindAddr string
	BindPort uint32
}

type forwardChannelData struct {
	DestAddr   string
	DestPort   uint32
	OriginAddr string
	OriginPort uint32
}

func (s *Service) forwardHandler(ctx ssh.Context, srv *ssh.Server, req *gossh.Request) (bool, []byte) {
	var reqPayload forwardReqPayload
	if err := gossh.Unmarshal(req.Payload, &reqPayload); err != nil {
		s.log.Error("failed to parse forward request payload", slog.String("err", err.Error()))
		return false, nil
	}

	conn := ctx.Value(ssh.ContextKeyConn).(*gossh.ServerConn)

	s.log.Info("received forward request",
		slog.String("remoteAddr", conn.RemoteAddr().String()),
		slog.String("localAddr", conn.LocalAddr().String()),
		slog.Any("payload", reqPayload),
	)

	s.mut.Lock()
	tunnel, ok := s.tunnels[reqPayload.BindAddr]
	s.mut.Unlock()
	if !ok {
		s.log.Warn("tunnel not found", slog.String("addr", reqPayload.BindAddr))
		return false, nil
	}

	s.mut.Lock()
	tunnel.conn = conn
	s.mut.Unlock()

	ch, reqs, err := tunnel.conn.OpenChannel("forwarded-tcpip", gossh.Marshal(&forwardChannelData{
		DestAddr: reqPayload.BindAddr,
		DestPort: 8080,
	}))
	if err != nil {
		s.log.Error("failed to open SSH channel", slog.String("err", err.Error()))
		return false, nil
	}
	defer ch.Close()

	go gossh.DiscardRequests(reqs)

	// Probe whether local service is running HTTP or HTTPs
	client := tunnel.getHTTPClient(ch)

	resp, err := client.Head(fmt.Sprintf("https://%s", tunnel.localAddr))
	if errors.Is(err, http.ErrSchemeMismatch) {
		slog.Info("service is running plain HTTP")
		s.mut.Lock()
		tunnel.scheme = "http"
		s.mut.Unlock()
		return true, nil
	} else if err != nil {
		// TODO: consider not failing here but probe on first request
		slog.Error("failed to probe local service", slog.String("err", err.Error()))
		return false, nil
	}

	slog.Info("service is running HTTPs")
	s.mut.Lock()
	tunnel.scheme = "https"
	s.mut.Unlock()
	defer resp.Body.Close()

	return true, nil
}

func (s *Service) cancelForwardHandler(ctx ssh.Context, srv *ssh.Server, req *gossh.Request) (ok bool, payload []byte) {
	s.log.Info("got cancel request!")

	var reqPayload forwardReqPayload
	if err := gossh.Unmarshal(req.Payload, &reqPayload); err != nil {
		s.log.Error("failed to parse forward request payload", slog.String("err", err.Error()))
		return false, []byte{}
	}

	s.mut.Lock()
	delete(s.tunnels, reqPayload.BindAddr)
	s.mut.Unlock()

	return true, nil
}

type sshConn struct {
	ch   gossh.Channel
	conn gossh.Conn
}

func (c *sshConn) Close() error {
	return c.ch.Close()
}

func (c *sshConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *sshConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

func (c *sshConn) Read(b []byte) (n int, err error) {
	return c.ch.Read(b)
}

func (c *sshConn) Write(b []byte) (n int, err error) {
	return c.ch.Write(b)
}

func (c *sshConn) SetDeadline(time.Time) error {
	return nil
}

func (c *sshConn) SetReadDeadline(time.Time) error {
	return nil
}

func (c *sshConn) SetWriteDeadline(time.Time) error {
	return nil
}
