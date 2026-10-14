package discovery

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"sort"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
)

const DirectoryProtocol protocol.ID = "/meshway/server-directory/1.0.0"
const maxDirectory = 16

type directoryHint struct {
	PeerID    string    `json:"peer_id"`
	Addresses []string  `json:"addresses"`
	Expires   time.Time `json:"expires"`
	Hops      int       `json:"hops"`
}
type directoryResponse struct {
	Servers []directoryHint `json:"servers"`
}

// TrustPeer marks a manually supplied/bootstrap transport as a directory source.
// Learned servers do not become sources, so this is a one-hop invitation graph.
func (s *Service) TrustPeer(id peer.ID) {
	if id == "" || id == s.host.ID() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.trusted) < 128 {
		s.trusted[id] = true
		delete(s.learned, id)
	}
}

func (s *Service) discoverLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
			s.RefreshDiscovery(ctx)
			cancel()
		}
	}
}

// RefreshDiscovery queries only explicit directory sources. Hints have no
// authority: each learned peer must answer its own authenticated advertisement.
func (s *Service) RefreshDiscovery(ctx context.Context) {
	if !s.options.AutoDiscover {
		return
	}
	s.mu.Lock()
	sources := make([]peer.ID, 0, len(s.trusted))
	for id := range s.trusted {
		sources = append(sources, id)
	}
	s.mu.Unlock()
	sort.Slice(sources, func(i, j int) bool { return sources[i].String() < sources[j].String() })
	if len(sources) > 8 {
		sources = sources[:8]
	}
	for _, source := range sources {
		if ctx.Err() != nil {
			return
		}
		hints, err := s.queryDirectory(ctx, source)
		if err != nil {
			continue
		}
		for _, hint := range hints {
			if ctx.Err() != nil {
				return
			}
			id, err := peer.Decode(hint.PeerID)
			if err != nil || id == s.host.ID() {
				continue
			}
			s.mu.Lock()
			_, cached := s.cache[id]
			cacheFull := len(s.cache) >= 128
			s.mu.Unlock()
			if cacheFull && !cached {
				continue
			}
			// DNSmultiaddresses are excluded: their resolution might point inside the
			// reader's LAN. Private literals are used only when that exact destination
			// was already in the local peerstore through a deliberate invitation.
			addresses := []ma.Multiaddr{}
			for _, text := range hint.Addresses {
				address, err := safeDirectoryAddress(text, id)
				if err != nil {
					continue
				}
				if !isPublicMultiaddr(address) && !s.knownAddress(id, address) {
					continue
				}
				addresses = append(addresses, address)
			}
			if len(addresses) == 0 {
				continue
			}
			s.host.Peerstore().AddAddrs(id, addresses, 5*time.Minute)
			s.mu.Lock()
			if len(s.learned) < 128 {
				hint.Hops++
				s.learned[id] = hint
			}
			s.mu.Unlock()
			verified, err := s.Server(ctx, id)
			if err != nil || verified.Personal {
				continue
			}
		}
	}
}
func (s *Service) knownAddress(id peer.ID, address ma.Multiaddr) bool {
	s.mu.Lock()
	_, learned := s.learned[id]
	trusted := s.trusted[id]
	s.mu.Unlock()
	if learned && !trusted {
		return false
	}
	for _, known := range s.host.Peerstore().Addrs(id) {
		if known.Equal(address) {
			return true
		}
	}
	return false
}
func (s *Service) shareableAddresses(id peer.ID, texts []string) []string {
	out := []string{}
	for _, text := range texts {
		address, err := safeDirectoryAddress(text, id)
		if err != nil {
			continue
		}
		if isPublicMultiaddr(address) {
			out = append(out, text)
		}
	}
	if len(out) > 4 {
		out = out[:4]
	}
	return out
}
func safeDirectoryAddress(text string, id peer.ID) (ma.Multiaddr, error) {
	if len(text) > 512 {
		return nil, errors.New("directory address too long")
	}
	info, err := peer.AddrInfoFromString(text)
	if err != nil || info.ID != id || len(info.Addrs) != 1 {
		return nil, errors.New("directory identity mismatch")
	}
	address := info.Addrs[0]
	ipCount := 0
	transport := false
	for _, component := range address.Protocols() {
		switch component.Code {
		case ma.P_IP4, ma.P_IP6:
			ipCount++
		case ma.P_TCP, ma.P_UDP:
			transport = true
		case ma.P_QUIC_V1, ma.P_WS, ma.P_WSS:
		default:
			return nil, errors.New("directory allows only literal direct IP transport")
		}
	}
	if ipCount != 1 || !transport {
		return nil, errors.New("directory requires one literal IP transport")
	}
	return address, nil
}
func isPublicMultiaddr(address ma.Multiaddr) bool {
	text, err := address.ValueForProtocol(ma.P_IP4)
	if err != nil {
		text, err = address.ValueForProtocol(ma.P_IP6)
	}
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(text)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32"} {
		if netip.MustParsePrefix(prefix).Contains(ip) {
			return false
		}
	}
	return true
}

func (s *Service) handleDirectory(stream network.Stream) {
	if !s.register(stream) {
		stream.Reset()
		return
	}
	defer s.remove(stream)
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(3 * time.Second))
	id := stream.Conn().RemotePeer()
	now := time.Now()
	s.mu.Lock()
	if !s.lastDirectory[id].IsZero() && now.Sub(s.lastDirectory[id]) < 10*time.Second {
		s.mu.Unlock()
		stream.Reset()
		return
	}
	if len(s.lastDirectory) >= 128 {
		for old, last := range s.lastDirectory {
			if now.Sub(last) > time.Minute {
				delete(s.lastDirectory, old)
			}
		}
		if len(s.lastDirectory) >= 128 {
			s.mu.Unlock()
			stream.Reset()
			return
		}
	}
	s.lastDirectory[id] = now
	hints := []directoryHint{}
	for peerID, hint := range s.directory {
		if now.After(hint.Expires) || hint.Hops >= 2 {
			delete(s.directory, peerID)
			continue
		}
		hints = append(hints, hint)
	}
	s.mu.Unlock()
	// Personal advertisements are never relayed. A public local server can
	// include itself without having previously queried its own advertisement.
	if s.options.Advertise && !s.options.Personal {
		addresses := []string{}
		for _, a := range s.host.Addrs() {
			addresses = append(addresses, a.String()+"/p2p/"+s.host.ID().String())
		}
		addresses = s.shareableAddresses(s.host.ID(), addresses)
		if len(addresses) > 0 {
			hints = append(hints, directoryHint{PeerID: s.host.ID().String(), Addresses: addresses, Expires: now.Add(5 * time.Minute)})
		}
	}
	sort.Slice(hints, func(i, j int) bool { return hints[i].PeerID < hints[j].PeerID })
	if len(hints) > maxDirectory {
		hints = hints[:maxDirectory]
	}
	body, _ := json.Marshal(directoryResponse{Servers: hints})
	if len(body) > 32768 {
		stream.Reset()
		return
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(body)))
	stream.Write(length[:])
	stream.Write(body)
}
func (s *Service) queryDirectory(parent context.Context, id peer.ID) ([]directoryHint, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	stream, err := s.host.NewStream(network.WithAllowLimitedConn(ctx, "invited server directory"), id, DirectoryProtocol)
	if err != nil {
		return nil, err
	}
	if !s.register(stream) {
		stream.Reset()
		return nil, errors.New("directory busy")
	}
	defer s.remove(stream)
	defer stream.Close()
	stop := context.AfterFunc(ctx, func() { stream.Reset() })
	defer stop()
	deadline, _ := ctx.Deadline()
	stream.SetDeadline(deadline)
	var length [4]byte
	if _, err = io.ReadFull(stream, length[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(length[:])
	if size == 0 || size > 32768 {
		return nil, errors.New("directory frame exceeds bound")
	}
	body := make([]byte, size)
	if _, err = io.ReadFull(stream, body); err != nil {
		return nil, err
	}
	var response directoryResponse
	if err = json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	if len(response.Servers) > maxDirectory {
		return nil, errors.New("too many directory hints")
	}
	now := time.Now()
	out := []directoryHint{}
	for _, hint := range response.Servers {
		if len(hint.Addresses) == 0 || len(hint.Addresses) > 4 || hint.Expires.Before(now) || hint.Expires.After(now.Add(5*time.Minute+5*time.Second)) {
			continue
		}
		if hint.Hops < 0 || hint.Hops >= 2 {
			continue
		}
		out = append(out, hint)
	}
	return out, nil
}
