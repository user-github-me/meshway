package core

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

func validVPNSession(value string) bool {
	if value == "" {
		return true
	}
	if len(value) != 32 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func (n *Node) packetAPIOwner(w http.ResponseWriter, r *http.Request) bool {
	session := r.Header.Get("X-Meshway-VPN-Session")
	if !validVPNSession(session) {
		apiError(w, 400, "invalid packet helper session ID")
		return false
	}
	if n.vpnOwner != session {
		apiError(w, 409, "packet session belongs to a different native client")
		return false
	}
	return true
}

func (n *Node) vpnAPI(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/v1/vpn/read":
		if r.Method != "POST" {
			apiError(w, 405, "method not allowed")
			return true
		}
		// Bound simultaneous helper polls using the same per-node admission
		// budget as proxy sessions. No detached goroutine waits for a packet.
		select {
		case n.sessions <- struct{}{}:
			defer func() { <-n.sessions }()
		default:
			apiError(w, 429, "packet helper is busy")
			return true
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		n.vpnMu.RLock()
		if !n.packetAPIOwner(w, r) {
			n.vpnMu.RUnlock()
			return true
		}
		if n.vpn == nil {
			n.vpnMu.RUnlock()
			apiError(w, 400, "packet service unavailable")
			return true
		}
		packet, err := n.vpn.ReadPacketContext(ctx)
		n.vpnMu.RUnlock()
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			w.WriteHeader(http.StatusNoContent)
		} else if err != nil {
			apiError(w, 400, err.Error())
		} else {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(packet)
		}
		return true
	case "/v1/vpn/write":
		if r.Method != "POST" {
			apiError(w, 405, "method not allowed")
			return true
		}
		select {
		case n.sessions <- struct{}{}:
			defer func() { <-n.sessions }()
		default:
			apiError(w, 429, "packet helper is busy")
			return true
		}
		if r.Header.Get("Content-Type") != "application/octet-stream" {
			apiError(w, 415, "packet body requires application/octet-stream")
			return true
		}
		r.Body = http.MaxBytesReader(w, r.Body, 9000)
		packet, err := io.ReadAll(r.Body)
		if err != nil {
			apiError(w, 400, "packet exceeds supported size")
			return true
		}
		n.vpnMu.RLock()
		defer n.vpnMu.RUnlock()
		if !n.packetAPIOwner(w, r) {
			return true
		}
		if n.vpn == nil {
			apiError(w, 400, "packet service unavailable")
			return true
		}
		if err = n.vpn.WritePacket(packet); err != nil {
			apiError(w, 400, err.Error())
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
		return true
	case "/v1/vpn/status":
		if r.Method != "GET" {
			apiError(w, 405, "method not allowed")
		} else {
			n.vpnMu.RLock()
			defer n.vpnMu.RUnlock()
			if !n.packetAPIOwner(w, r) {
				return true
			}
			jsonResponse(w, 200, n.VPNStatus())
		}
		return true
	case "/v1/vpn/stop":
		if r.Method != "POST" {
			apiError(w, 405, "method not allowed")
			return true
		}
		n.vpnMu.Lock()
		defer n.vpnMu.Unlock()
		if !n.packetAPIOwner(w, r) {
			return true
		}
		n.vpnOwner = ""
		if n.vpn == nil {
			jsonResponse(w, 200, map[string]bool{"ok": true})
			return true
		}
		if err := n.vpn.StopPacketVPN(); err != nil {
			apiError(w, 400, err.Error())
		} else {
			jsonResponse(w, 200, map[string]bool{"ok": true})
		}
		return true
	case "/v1/vpn/start":
		if r.Method != "POST" {
			apiError(w, 405, "method not allowed")
			return true
		}
		var body struct {
			PeerID    string `json:"peer_id"`
			Device    string `json:"device"`
			Mode      string `json:"mode"`
			SessionID string `json:"session_id,omitempty"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			apiError(w, 400, "invalid request body")
			return true
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			apiError(w, 400, "invalid request body")
			return true
		}
		var err error
		n.vpnMu.Lock()
		defer n.vpnMu.Unlock()
		session := body.SessionID
		if !validVPNSession(session) || r.Header.Get("X-Meshway-VPN-Session") != session {
			apiError(w, 400, "invalid packet helper session ID")
			return true
		}
		if n.vpn == nil {
			apiError(w, 400, "packet service unavailable")
			return true
		}
		if n.vpnOwner != "" && n.vpnOwner != session {
			apiError(w, 409, "packet session belongs to another native helper")
			return true
		}
		if session != "" && n.vpnOwner == "" && n.vpn.Status().PeerID != "" {
			apiError(w, 409, "a native VPN device is already attached")
			return true
		}
		switch body.Mode {
		case "exit":
			if body.Device == "" {
				p := body.PeerID
				if p == "" {
					p = n.UpstreamPeer()
				}
				n.vpnOwner = session
				err = n.vpn.StartPacketVPN(p)
			} else {
				if session != "" {
					apiError(w, 400, "helper sessions require packet queue mode")
					return true
				}
				p := body.PeerID
				if p == "" {
					p = n.UpstreamPeer()
				}
				err = n.vpn.StartDeviceExit(body.Device, p)
			}
		case "":
			apiError(w, 400, "mode must be exit")
			return true
		default:
			apiError(w, 400, "unsupported VPN mode")
			return true
		}
		if err != nil {
			if n.vpn.Status().PeerID == "" {
				n.vpnOwner = ""
			}
			apiError(w, 400, err.Error())
		} else {
			jsonResponse(w, 200, n.VPNStatus())
		}
		return true
	default:
		return false
	}
}
