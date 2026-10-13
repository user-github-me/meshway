package core

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

func jsonResponse(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func apiError(w http.ResponseWriter, code int, msg string) {
	jsonResponse(w, code, map[string]string{"error": msg})
}

func (n *Node) apiHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Defend against DNS rebinding, cross-site requests, and network exposure.
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			apiError(w, 403, "invalid host")
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" {
			allowed := false
			for _, v := range n.Config.AllowedOrigins {
				if origin == v {
					allowed = true
				}
			}
			if !allowed {
				apiError(w, 403, "origin is not allowed")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if provided == r.Header.Get("Authorization") || subtle.ConstantTimeCompare([]byte(provided), []byte(n.Config.APIToken)) != 1 {
			apiError(w, 401, "bearer token required")
			return
		}
		switch r.URL.Path {
		case "/v1/disconnect":
			if r.Method != "POST" {
				apiError(w, 405, "method not allowed")
				return
			}
			n.Disconnect()
			jsonResponse(w, 200, map[string]any{"ok": true, "upstream_peer": ""})
		case "/v1/servers":
			if r.Method != "GET" {
				apiError(w, 405, "method not allowed")
				return
			}
			jsonResponse(w, 200, map[string]any{"servers": n.Discovery.Servers(r.Context())})
		case "/v1/select":
			if r.Method != "POST" {
				apiError(w, 405, "method not allowed")
				return
			}
			var body struct {
				PeerID  string `json:"peer_id"`
				Address string `json:"address"`
			}
			r.Body = http.MaxBytesReader(w, r.Body, 8192)
			d := json.NewDecoder(r.Body)
			d.DisallowUnknownFields()
			if err := d.Decode(&body); err != nil {
				apiError(w, 400, "invalid request body")
				return
			}
			var extra any
			if err := d.Decode(&extra); err != io.EOF {
				apiError(w, 400, "invalid request body")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			if err := n.SelectPeer(ctx, body.PeerID, body.Address); err != nil {
				apiError(w, 400, err.Error())
				return
			}
			jsonResponse(w, 200, map[string]any{"ok": true, "upstream_peer": n.UpstreamPeer()})
		case "/v1/account/status":
			if r.Method != "GET" {
				apiError(w, 405, "method not allowed")
				return
			}
			jsonResponse(w, 200, n.Account.Status())
		case "/v1/account/devices":
			if r.Method != "GET" {
				apiError(w, 405, "method not allowed")
				return
			}
			jsonResponse(w, 200, map[string]any{"devices": n.Account.Devices()})
		case "/v1/account/history":
			if r.Method != "GET" {
				apiError(w, 405, "method not allowed")
				return
			}
			jsonResponse(w, 200, map[string]any{"events": n.Account.History()})
		case "/v1/account/sync":
			if r.Method != "POST" {
				apiError(w, 405, "method not allowed")
				return
			}
			var body struct {
				PeerID string `json:"peer_id"`
			}
			r.Body = http.MaxBytesReader(w, r.Body, 8192)
			d := json.NewDecoder(r.Body)
			d.DisallowUnknownFields()
			if err := d.Decode(&body); err != nil {
				apiError(w, 400, "invalid request body")
				return
			}
			var extra any
			if err := d.Decode(&extra); err != io.EOF {
				apiError(w, 400, "invalid request body")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			if err := n.Account.Sync(ctx, body.PeerID); err != nil {
				apiError(w, 400, err.Error())
				return
			}
			jsonResponse(w, 200, map[string]bool{"ok": true})
		case "/v1/account/revoke":
			if r.Method != "POST" {
				apiError(w, 405, "method not allowed")
				return
			}
			var body struct {
				PeerID string `json:"peer_id"`
			}
			r.Body = http.MaxBytesReader(w, r.Body, 8192)
			d := json.NewDecoder(r.Body)
			d.DisallowUnknownFields()
			if err := d.Decode(&body); err != nil {
				apiError(w, 400, "invalid request body")
				return
			}
			var extra any
			if err := d.Decode(&extra); err != io.EOF {
				apiError(w, 400, "invalid request body")
				return
			}
			if err := n.Account.Revoke(body.PeerID); err != nil {
				apiError(w, 400, err.Error())
				return
			}
			jsonResponse(w, 200, map[string]bool{"ok": true})
		case "/v1/status":
			if r.Method != "GET" {
				apiError(w, 405, "method not allowed")
				return
			}
			jsonResponse(w, 200, n.Status())
		case "/v1/peers":
			if r.Method != "GET" {
				apiError(w, 405, "method not allowed")
				return
			}
			jsonResponse(w, 200, map[string]any{"peers": n.Status().Peers})
		case "/v1/connect":
			if r.Method != "POST" {
				apiError(w, 405, "method not allowed")
				return
			}
			var body struct {
				Address string `json:"address"`
			}
			r.Body = http.MaxBytesReader(w, r.Body, 8192)
			d := json.NewDecoder(r.Body)
			d.DisallowUnknownFields()
			if err := d.Decode(&body); err != nil {
				apiError(w, 400, "invalid request body")
				return
			}
			var extra any
			if err := d.Decode(&extra); err != io.EOF {
				apiError(w, 400, "invalid request body")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			p, err := n.Connect(ctx, body.Address)
			if err != nil {
				apiError(w, 400, err.Error())
				return
			}
			jsonResponse(w, 200, p)
		case "/v1/shutdown":
			if r.Method != "POST" {
				apiError(w, 405, "method not allowed")
				return
			}
			jsonResponse(w, 200, map[string]bool{"ok": true})
			go func() { time.Sleep(100 * time.Millisecond); n.Stop() }()
		default:
			apiError(w, 404, "not found")
		}
	})
}
