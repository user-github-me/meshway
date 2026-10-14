package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/user-github-me/meshway/internal/state"
)

type preferences struct {
	Servers  []string `json:"servers"`
	Selected string   `json:"selected,omitempty"`
}

func readPreferences(dir string, key []byte) (preferences, error) {
	var p preferences
	b, err := state.ReadPrivate(filepath.Join(dir, "preferences.json"), 128<<10, key)
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if json.Unmarshal(b, &p) != nil || len(p.Servers) > 128 {
		return p, errors.New("invalid local server preferences")
	}
	for _, address := range p.Servers {
		if len(address) > 4096 {
			return p, errors.New("invalid saved server address")
		}
		if _, err := peer.AddrInfoFromString(address); err != nil {
			return p, err
		}
	}
	if p.Selected != "" {
		if _, err := peer.Decode(p.Selected); err != nil {
			return p, err
		}
	}
	return p, nil
}

func (n *Node) savePreferences(address string) error {
	n.preferencesMu.Lock()
	defer n.preferencesMu.Unlock()
	p, err := readPreferences(n.Config.StateDir, n.Config.StorageKey)
	if err != nil {
		return err
	}
	if address != "" {
		found := false
		for _, old := range p.Servers {
			if old == address {
				found = true
				break
			}
		}
		if !found {
			if len(p.Servers) >= 128 {
				return errors.New("saved server limit reached")
			}
			p.Servers = append(p.Servers, address)
			sort.Strings(p.Servers)
		}
	}
	p.Selected = n.UpstreamPeer()
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return state.SavePrivate(filepath.Join(n.Config.StateDir, "preferences.json"), b, n.Config.StorageKey)
}
