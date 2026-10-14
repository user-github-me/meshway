package core

import (
	"context"
	"errors"
)

func (n *Node) StartPacketVPN(peerID string) error {
	n.vpnMu.Lock()
	defer n.vpnMu.Unlock()
	if n.vpnOwner != "" {
		return errors.New("packet VPN is owned by a native helper")
	}
	if n.vpn == nil {
		return errors.New("packet service unavailable")
	}
	if peerID == "" {
		peerID = n.UpstreamPeer()
	}
	return n.vpn.StartPacketVPN(peerID)
}
func (n *Node) ReadPacket() ([]byte, error) {
	return n.ReadPacketContext(context.Background())
}
func (n *Node) ReadPacketContext(ctx context.Context) ([]byte, error) {
	n.vpnMu.RLock()
	if n.vpnOwner != "" {
		n.vpnMu.RUnlock()
		return nil, errors.New("packet VPN is owned by a native helper")
	}
	// Embedded native clients can block until StopPacketVPN. They use their
	// own Node, so no ownership handoff to an HTTP helper is possible mid-read.
	n.vpnMu.RUnlock()
	if n.vpn == nil {
		return nil, errors.New("packet service unavailable")
	}
	return n.vpn.ReadPacketContext(ctx)
}
func (n *Node) WritePacket(packet []byte) error {
	n.vpnMu.RLock()
	defer n.vpnMu.RUnlock()
	if n.vpnOwner != "" {
		return errors.New("packet VPN is owned by a native helper")
	}
	if n.vpn == nil {
		return errors.New("packet service unavailable")
	}
	return n.vpn.WritePacket(packet)
}
func (n *Node) StopPacketVPN() error {
	n.vpnMu.Lock()
	defer n.vpnMu.Unlock()
	n.vpnOwner = ""
	if n.vpn == nil {
		return nil
	}
	return n.vpn.StopPacketVPN()
}
func (n *Node) StartInternetVPN(fd int, peerID string) error {
	n.vpnMu.Lock()
	defer n.vpnMu.Unlock()
	if n.vpnOwner != "" {
		return errors.New("packet VPN is owned by a native helper")
	}
	if n.vpn == nil {
		return errors.New("packet service unavailable")
	}
	if peerID == "" {
		peerID = n.UpstreamPeer()
	}
	return n.vpn.AttachExitFD(fd, peerID)
}
func (n *Node) StartDeviceVPN(name, peerID string) error {
	n.vpnMu.Lock()
	defer n.vpnMu.Unlock()
	if n.vpnOwner != "" {
		return errors.New("packet VPN is owned by a native helper")
	}
	if n.vpn == nil {
		return errors.New("packet service unavailable")
	}
	if peerID == "" {
		peerID = n.UpstreamPeer()
	}
	return n.vpn.StartDeviceExit(name, peerID)
}
func (n *Node) VPNStatus() any {
	if n.vpn == nil {
		return nil
	}
	return n.vpn.Status()
}
