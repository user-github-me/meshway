package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync/atomic"
	"time"
)

const TunnelProtocol = "/meshway/tcp/1.0.0"
const maxHeader = 4096
const handshakeTimeout = 10 * time.Second

// A peer handshake includes encrypted transport setup, account authorization,
// exit-side DNS and a target connection. Those independently bounded operations
// must fit inside the total budget rather than sharing the target's ten seconds.
const peerHandshakeTimeout = 30 * time.Second
const idleTimeout = 2 * time.Minute

type request struct {
	Target  string `json:"target,omitempty"`
	Service string `json:"service,omitempty"`
}
type response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func writeFrame(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > maxHeader {
		return errors.New("frame too large")
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(b)))
	if _, err = w.Write(size[:]); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
func readFrame(r io.Reader, v any) error {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(size[:])
	if n == 0 || n > maxHeader {
		return errors.New("invalid frame size")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("frame must contain one JSON object")
	}
	return nil
}

var reserved = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001:db8::/32"),
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	for _, p := range reserved {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

func dialTarget(ctx context.Context, target string, allowPrivate bool) (net.Conn, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return nil, errors.New("target must be host:port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return nil, errors.New("invalid target port")
	}
	if host == "" {
		return nil, errors.New("target host required")
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("target has no addresses")
	}
	for _, ip := range addresses {
		if !allowPrivate && !publicIP(ip) {
			return nil, errors.New("private or reserved destination is disabled")
		}
	}
	d := net.Dialer{Timeout: handshakeTimeout}
	c, err := dialAddresses(ctx, addresses, port, d.DialContext)
	if err != nil {
		return nil, fmt.Errorf("connect target: %w", err)
	}
	return c, nil
}

// Race only the previously validated literal IPs. Staggering the other family
// avoids an unreachable IPv6 route consuming the whole connection deadline.
func dialAddresses(ctx context.Context, addresses []netip.Addr, port string, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	if len(addresses) > 32 {
		return nil, errors.New("too many resolved destination addresses")
	}
	if len(addresses) == 0 {
		return nil, errors.New("target has no addresses")
	}
	var first, other []netip.Addr
	family4 := addresses[0].Unmap().Is4()
	for _, ip := range addresses {
		if ip.Unmap().Is4() == family4 {
			first = append(first, ip)
		} else {
			other = append(other, ip)
		}
	}
	ordered := make([]netip.Addr, 0, len(addresses))
	for len(first) > 0 || len(other) > 0 {
		if len(first) > 0 {
			ordered = append(ordered, first[0])
			first = first[1:]
		}
		if len(other) > 0 {
			ordered = append(ordered, other[0])
			other = other[1:]
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		conn net.Conn
		err  error
	}
	results := make(chan result)
	for i, ip := range ordered {
		go func(i int, ip netip.Addr) {
			if i > 0 {
				timer := time.NewTimer(time.Duration(i) * 250 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-ctx.Done():
					return
				}
			}
			c, err := dial(ctx, "tcp", net.JoinHostPort(ip.Unmap().String(), port))
			select {
			case results <- result{c, err}:
			case <-ctx.Done():
				if c != nil {
					c.Close()
				}
			}
		}(i, ip)
	}
	var last error
	for range ordered {
		select {
		case r := <-results:
			if r.err == nil && r.conn != nil {
				return r.conn, nil
			}
			last = r.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if last == nil {
		last = errors.New("destination dialing failed")
	}
	return nil, last
}

type countingWriter struct {
	io.Writer
	counter *atomic.Uint64
}

func (w countingWriter) Write(b []byte) (int, error) {
	n, err := w.Writer.Write(b)
	w.counter.Add(uint64(n))
	return n, err
}

type deadlineReadWriter interface {
	io.Reader
	io.Writer
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
}
type activityConn struct{ deadlineReadWriter }

func (c activityConn) Read(b []byte) (int, error) {
	_ = c.SetReadDeadline(time.Now().Add(idleTimeout))
	return c.deadlineReadWriter.Read(b)
}
func (c activityConn) Write(b []byte) (int, error) {
	_ = c.SetWriteDeadline(time.Now().Add(idleTimeout))
	return c.deadlineReadWriter.Write(b)
}
