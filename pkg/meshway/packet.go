package meshway

import "encoding/json"

// StartPacketVPN opens an authenticated internet packet tunnel. ReadPacket and
// WritePacket connect native NetworkExtension packet flow without raw OS handles.
func (n *Node) StartPacketVPN(peerID string) error { return n.node.StartPacketVPN(peerID) }
func (n *Node) ReadPacket() ([]byte, error)        { return n.node.ReadPacket() }
func (n *Node) WritePacket(packet []byte) error    { return n.node.WritePacket(packet) }
func (n *Node) StopPacketVPN() error               { return n.node.StopPacketVPN() }
func (n *Node) StartInternetVPN(fd int, peerID string) error {
	return n.node.StartInternetVPN(fd, peerID)
}
func (n *Node) VPNStatus() (string, error) {
	b, err := json.Marshal(n.node.VPNStatus())
	return string(b), err
}
