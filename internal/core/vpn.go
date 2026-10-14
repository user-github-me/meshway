package core

import "errors"

func (n *Node) StartPacketVPN(peerID string) error {
	if n.vpn == nil {
		return errors.New("packet service unavailable")
	}
	if peerID == "" {
		peerID = n.UpstreamPeer()
	}
	return n.vpn.StartPacketVPN(peerID)
}
func (n *Node) ReadPacket() ([]byte, error) {
	if n.vpn == nil {
		return nil, errors.New("packet service unavailable")
	}
	return n.vpn.ReadPacket()
}
func (n *Node) WritePacket(packet []byte) error {
	if n.vpn == nil {
		return errors.New("packet service unavailable")
	}
	return n.vpn.WritePacket(packet)
}
func (n *Node) StopPacketVPN() error {
	if n.vpn == nil {
		return nil
	}
	return n.vpn.StopPacketVPN()
}
func (n *Node) StartInternetVPN(fd int, peerID string) error {
	if n.vpn == nil {
		return errors.New("packet service unavailable")
	}
	if peerID == "" {
		peerID = n.UpstreamPeer()
	}
	return n.vpn.AttachExitFD(fd, peerID)
}
func (n *Node) StartDeviceVPN(name, peerID string) error {
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
