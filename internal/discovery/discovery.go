// Package discovery reads authenticated, opt-in server advertisements from
// explicit configured and connected peers. It does not run a public directory.
package discovery

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const Protocol protocol.ID = "/meshway/server-info/1.0.0"

type Roles struct {
	Relay bool `json:"relay"`
	Exit  bool `json:"exit"`
}
type Server struct {
	Personal   bool      `json:"personal"`
	PeerID     string    `json:"peer_id"`
	Label      string    `json:"label"`
	Country    string    `json:"country,omitempty"`
	Address    string    `json:"address"`
	Addresses  []string  `json:"addresses"`
	Roles      Roles     `json:"roles"`
	Available  bool      `json:"available"`
	CanConnect bool      `json:"can_connect"`
	LastSeen   time.Time `json:"last_seen"`
}
type Options struct {
	Personal     bool
	Authorize    func(context.Context, peer.ID) bool
	Advertise    bool
	Label        string
	Country      string
	Relay        bool
	Exit         bool
	AllowedPeers []string
}
type Service struct {
	host    host.Host
	options Options
	mu      sync.Mutex
	cache   map[peer.ID]Server
	pending map[network.Stream]bool
	closed  bool
}

func New(h host.Host, options Options) (*Service, error) {
	if h == nil {
		return nil, errors.New("host required")
	}
	if len(options.Label) > 80 {
		return nil, errors.New("server label must be at most 80 bytes")
	}
	if options.Country != "" && (len(options.Country) != 2 || strings.ToUpper(options.Country) != options.Country || options.Country[0] < 'A' || options.Country[0] > 'Z' || options.Country[1] < 'A' || options.Country[1] > 'Z') {
		return nil, errors.New("country must be a two-letter uppercase country code")
	}
	s := &Service{host: h, options: options, cache: map[peer.ID]Server{}, pending: map[network.Stream]bool{}}
	if options.Advertise {
		h.SetStreamHandler(Protocol, s.handle)
	}
	return s, nil
}

func ValidateAddress(address string, id peer.ID) error {
	info, err := peer.AddrInfoFromString(address)
	if err != nil {
		return err
	}
	if info.ID != id {
		return errors.New("address peer ID mismatch")
	}
	return nil
}
func (s *Service) register(stream network.Stream) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || len(s.pending) >= 32 {
		return false
	}
	s.pending[stream] = true
	return true
}
func (s *Service) remove(stream network.Stream) {
	s.mu.Lock()
	delete(s.pending, stream)
	s.mu.Unlock()
}
func (s *Service) handle(stream network.Stream) {
	if !s.register(stream) {
		stream.Reset()
		return
	}
	defer s.remove(stream)
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(3 * time.Second))
	id := stream.Conn().RemotePeer()
	if s.options.Personal {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		approved := s.options.Authorize != nil && s.options.Authorize(ctx, id)
		cancel()
		if !approved {
			stream.Reset()
			return
		}
	}
	approved := false
	for _, p := range s.options.AllowedPeers {
		if p == id.String() {
			approved = true
		}
	}
	if s.options.Personal {
		approved = true
	}
	addresses := []string{}
	for _, a := range s.host.Addrs() {
		addresses = append(addresses, a.String()+"/p2p/"+s.host.ID().String())
	}
	sort.Strings(addresses)
	address := ""
	if len(addresses) > 0 {
		address = addresses[0]
	}
	label := s.options.Label
	if label == "" {
		label = "Community peer"
	}
	server := Server{Personal: s.options.Personal, PeerID: s.host.ID().String(), Label: label, Country: s.options.Country, Address: address, Addresses: addresses, Roles: Roles{Relay: s.options.Relay, Exit: s.options.Exit}, Available: true, CanConnect: s.options.Exit && approved, LastSeen: time.Now().UTC()}
	b, _ := json.Marshal(server)
	if len(b) > 16384 {
		stream.Reset()
		return
	}
	var frame [4]byte
	binary.BigEndian.PutUint32(frame[:], uint32(len(b)))
	_, _ = stream.Write(frame[:])
	_, _ = stream.Write(b)
}

func (s *Service) Server(ctx context.Context, id peer.ID) (Server, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	stream, err := s.host.NewStream(network.WithAllowLimitedConn(ctx, "server advertisement"), id, Protocol)
	if err != nil {
		return Server{}, err
	}
	if !s.register(stream) {
		stream.Reset()
		return Server{}, errors.New("server discovery is closed or busy")
	}
	defer s.remove(stream)
	defer stream.Close()
	stopCancel := context.AfterFunc(ctx, func() { stream.Reset() })
	defer stopCancel()
	deadline, _ := ctx.Deadline()
	stream.SetDeadline(deadline)
	var frame [4]byte
	if _, err = io.ReadFull(stream, frame[:]); err != nil {
		return Server{}, err
	}
	size := binary.BigEndian.Uint32(frame[:])
	if size == 0 || size > 16384 {
		return Server{}, errors.New("invalid advertisement size")
	}
	b := make([]byte, size)
	if _, err = io.ReadFull(stream, b); err != nil {
		return Server{}, err
	}
	var server Server
	if err = json.Unmarshal(b, &server); err != nil {
		return server, err
	}
	if server.PeerID != id.String() || len(server.Label) > 80 || len(server.Country) > 2 || len(server.Addresses) > 32 || len(server.Addresses) == 0 || server.Address == "" {
		return Server{}, errors.New("invalid advertisement identity or metadata")
	}
	for _, a := range server.Addresses {
		info, err := peer.AddrInfoFromString(a)
		if err != nil || info.ID != id {
			return Server{}, errors.New("advertised address peer ID mismatch")
		}
	}
	if server.Address != "" {
		info, err := peer.AddrInfoFromString(server.Address)
		if err != nil || info.ID != id {
			return Server{}, errors.New("advertised address peer ID mismatch")
		}
	}
	server.Available = true
	server.LastSeen = time.Now().UTC()
	s.mu.Lock()
	s.cache[id] = server
	s.mu.Unlock()
	return server, nil
}

func (s *Service) Servers(ctx context.Context) []Server {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ids := s.host.Peerstore().Peers()
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	if len(ids) > 33 {
		ids = ids[:33]
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, id := range ids {
		if id == s.host.ID() {
			continue
		}
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if _, err := s.Server(ctx, id); err != nil {
				s.mu.Lock()
				server, ok := s.cache[id]
				if ok {
					server.Available = false
					server.CanConnect = false
					s.cache[id] = server
				}
				s.mu.Unlock()
			}
		}()
	}
	wg.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Server{}
	for _, server := range s.cache {
		out = append(out, server)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PeerID < out[j].PeerID })
	return out
}

func (s *Service) Close() error {
	s.mu.Lock()
	s.closed = true
	for stream := range s.pending {
		stream.Reset()
	}
	s.mu.Unlock()
	s.host.RemoveStreamHandler(Protocol)
	return nil
}
