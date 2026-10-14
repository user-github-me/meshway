package core

import (
	"encoding/json"
	"io"
	"net/http"
)

func (n *Node) vpnAPI(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/v1/vpn/status":
		if r.Method != "GET" {
			apiError(w, 405, "method not allowed")
		} else {
			jsonResponse(w, 200, n.VPNStatus())
		}
		return true
	case "/v1/vpn/stop":
		if r.Method != "POST" {
			apiError(w, 405, "method not allowed")
			return true
		}
		if err := n.StopPacketVPN(); err != nil {
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
			PeerID string `json:"peer_id"`
			Device string `json:"device"`
			Mode   string `json:"mode"`
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
		switch body.Mode {
		case "exit":
			if body.Device == "" {
				err = n.StartPacketVPN(body.PeerID)
			} else {
				err = n.StartDeviceVPN(body.Device, body.PeerID)
			}
		case "":
			apiError(w, 400, "mode must be exit")
			return true
		default:
			apiError(w, 400, "unsupported VPN mode")
			return true
		}
		if err != nil {
			apiError(w, 400, err.Error())
		} else {
			jsonResponse(w, 200, n.VPNStatus())
		}
		return true
	default:
		return false
	}
}
