package core

import (
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"time"
)

func (n *Node) handleSOCKS(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(handshakeTimeout))
	var header [2]byte
	if _, err := io.ReadFull(c, header[:]); err != nil {
		return
	}
	if header[0] != 5 || header[1] == 0 {
		return
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(c, methods); err != nil {
		return
	}
	supported := false
	for _, m := range methods {
		if m == 0 {
			supported = true
		}
	}
	if !supported {
		_, _ = c.Write([]byte{5, 255})
		return
	}
	if _, err := c.Write([]byte{5, 0}); err != nil {
		return
	}
	var req [4]byte
	if _, err := io.ReadFull(c, req[:]); err != nil {
		return
	}
	if req[0] != 5 || req[2] != 0 {
		return
	}
	if req[1] != 1 && req[1] != 3 {
		socksReply(c, 7)
		return
	}
	var host string
	switch req[3] {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 4:
		b := make([]byte, 16)
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 3:
		var size [1]byte
		if _, err := io.ReadFull(c, size[:]); err != nil || size[0] == 0 {
			return
		}
		b := make([]byte, int(size[0]))
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = string(b)
	default:
		socksReply(c, 8)
		return
	}
	var port [2]byte
	if _, err := io.ReadFull(c, port[:]); err != nil {
		return
	}
	p := binary.BigEndian.Uint16(port[:])
	if req[1] == 3 {
		n.handleSOCKSUDP(c, host, p)
		return
	}
	if p == 0 {
		socksReply(c, 1)
		return
	}
	s, err := n.openTunnel(n.ctx, n.UpstreamPeer(), request{Target: net.JoinHostPort(host, strconv.Itoa(int(p)))})
	if err != nil {
		socksReply(c, 2)
		return
	}
	defer s.Close()
	if !socksReply(c, 0) {
		return
	}
	_ = c.SetDeadline(time.Time{})
	n.bridge(c, s)
}

func socksReply(c net.Conn, code byte) bool {
	_, err := c.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0})
	return err == nil
}
