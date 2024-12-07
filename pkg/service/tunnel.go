package service

import (
	"context"
	"log/slog"
	"net"
	"net/http"

	gossh "golang.org/x/crypto/ssh"
)

func (t *tunnel) getHTTPClient(ch gossh.Channel) http.Client {
	return http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				slog.Info("returning ssh channel conn")
				return &sshConn{
					ch:   ch,
					conn: t.conn,
				}, nil
			},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			slog.Info("redirect!")
			return http.ErrUseLastResponse
		},
	}
}
