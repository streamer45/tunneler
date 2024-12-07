package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"

	gossh "golang.org/x/crypto/ssh"
)

const (
	tunnelIDCookieName = "TUNNELID"
	bindPort           = 8080
)

type tunnelCreateReq struct {
	LocalAddr string
}

func (r tunnelCreateReq) IsValid() error {
	if r.LocalAddr == "" {
		return fmt.Errorf("invalid empty value")
	}

	if _, _, err := parseAddress(r.LocalAddr); err != nil {
		return fmt.Errorf("failed to parse LocalAddr: %w", err)
	}

	return nil
}

func generateTunnelCommand(tunnelID, laddr, hostname string, sshAddr string) string {
	// NOTE: here we make an assumption that if no port is provided we defauul to HTTPs (i.e. 443)
	_, localPort, _ := net.SplitHostPort(laddr)
	if localPort == "" {
		laddr += ":443"
	}
	_, sshPort, _ := net.SplitHostPort(sshAddr)
	return fmt.Sprintf("ssh -N -T -R%s:%d:%s %s -p %s", tunnelID, bindPort, laddr, hostname, sshPort)
}

func generateAccessURLs(tunnelID, httpAddr, httpsAddr string) []string {
	return []string{
		fmt.Sprintf("http://%s/tunnels/%s/", httpAddr, tunnelID),
		fmt.Sprintf("https://%s/tunnels/%s/", httpsAddr, tunnelID),
	}
}

type tunnelCreateRes struct {
	TunnelCommand string
	URLs          []string
}

func (s *Service) createAPIMux() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/tunnels", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var data tunnelCreateReq
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			s.log.Warn("decoding tunnelCreateReq failed", slog.String("err", err.Error()))
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if err := data.IsValid(); err != nil {
			http.Error(w, fmt.Sprintf("invalid data: %s", err.Error()), http.StatusBadRequest)
		}

		s.log.Info("tunnel creation", slog.Any("payload", data))

		tunnelID := newID()

		s.mut.Lock()
		s.tunnels[tunnelID] = &tunnel{
			localAddr: data.LocalAddr,
		}
		s.mut.Unlock()

		res := tunnelCreateRes{
			TunnelCommand: generateTunnelCommand(tunnelID, data.LocalAddr, s.cfg.Hostname, s.cfg.SSHAddr),
			URLs:          generateAccessURLs(tunnelID, s.httpLn.Addr().String(), s.httpsLn.Addr().String()),
		}
		if err := json.NewEncoder(w).Encode(res); err != nil {
			s.log.Error("failed to encode response", slog.String("err", err.Error()))
		}
	})

	mux.HandleFunc("/tunnels/{tunnelID}/", func(w http.ResponseWriter, r *http.Request) {
		s.log.Debug("handling HTTP request",
			slog.Any("host", r.Host),
			slog.Any("url", r.URL),
		)

		tunnelID := r.PathValue("tunnelID")

		s.mut.Lock()
		tunnel, ok := s.tunnels[tunnelID]
		s.mut.Unlock()
		if !ok {
			http.Error(w, "tunnel not found", http.StatusBadRequest)
			return
		}
		req := r.Clone(context.Background())
		req.Host = tunnel.localAddr
		req.URL.Path, _ = strings.CutPrefix(req.URL.Path, fmt.Sprintf("/tunnels/%s", tunnelID))

		srvAddr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
		if !ok {
			s.log.Error("failed to get server address")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		srvHost, _, _ := net.SplitHostPort(srvAddr.String())

		slog.Info(srvAddr.String())

		http.SetCookie(w, &http.Cookie{
			Name:     tunnelIDCookieName,
			Value:    tunnelID,
			MaxAge:   3600,
			Path:     "/",
			SameSite: http.SameSiteStrictMode,
			HttpOnly: true,
			Domain:   srvHost,
			Secure:   srvAddr.String() == s.cfg.HTTPSAddr,
		})

		http.Redirect(w, r, req.URL.String(), http.StatusFound)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.log.Debug("http request",
			slog.Any("host", r.Host),
			slog.Any("url", r.URL),
		)

		cookie, err := r.Cookie(tunnelIDCookieName)
		if err != nil || cookie.Value == "" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		tunnelID := cookie.Value

		s.mut.Lock()
		tunnel, ok := s.tunnels[tunnelID]
		s.mut.Unlock()
		if !ok {
			http.Error(w, "tunnel not found", http.StatusBadRequest)
			return
		}

		ch, reqs, err := tunnel.conn.OpenChannel("forwarded-tcpip", gossh.Marshal(&forwardChannelData{
			DestAddr: tunnelID,
			DestPort: bindPort,
		}))
		if err != nil {
			s.log.Error("failed to open SSH channel", slog.String("err", err.Error()))
			http.Error(w, "failed to open SSH tunnel", http.StatusInternalServerError)
			return
		}
		defer ch.Close()

		go gossh.DiscardRequests(reqs)

		// Handling WebSocket
		if val := r.Header.Get("upgrade"); val != "" {
			s.log.Info("upgrade requested")

			hj, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "webserver doesn't support hijacking", http.StatusInternalServerError)
				return
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer conn.Close()

			req := r.Clone(context.Background())
			req.Host = tunnel.localAddr
			req.URL.Path, _ = strings.CutPrefix(req.URL.Path, fmt.Sprintf("/tunnels/%s", tunnelID))

			if err := req.Write(ch); err != nil {
				s.log.Error("failed to write HTTP request", slog.String("err", err.Error()))
				return
			}

			go func() {
				if _, err := io.Copy(ch, conn); err != nil {
					slog.Warn("copy failed", slog.String("err", err.Error()))
				}
			}()

			if _, err := io.Copy(conn, ch); err != nil {
				slog.Warn("copy failed", slog.String("err", err.Error()))
			}

			return
		}

		client := tunnel.getHTTPClient(ch)

		req := r.Clone(context.Background())
		req.Host = tunnel.localAddr
		req.URL.Path, _ = strings.CutPrefix(req.URL.Path, fmt.Sprintf("/tunnels/%s", tunnelID))

		reqURL := fmt.Sprintf("%s://%s%s", tunnel.scheme, tunnel.localAddr, req.URL.String())

		creq, err := http.NewRequest(req.Method, reqURL, req.Body)
		if err != nil {
			s.log.Error("", slog.String("err", err.Error()))
			http.Error(w, "failed to create request", http.StatusInternalServerError)
			return
		}

		creq.Header = req.Header
		creq.Header.Set("connection", "close")
		creq.Close = true
		creq.Header.Del("accept-encoding") // disable compression for now

		// TODO: in case of a redirect, check whether the hostname matches localAddr, in which case we could try to
		// connect through the tunnel.

		resp, err := client.Do(creq)
		if err != nil {
			s.log.Error("request failed", slog.String("err", err.Error()))
			http.Error(w, "request failed", http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		clear(w.Header())
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)

		if _, err := io.Copy(w, resp.Body); err != nil {
			slog.Warn("copy failed", slog.String("err", err.Error()))
		}
	})

	return mux
}
