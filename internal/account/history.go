package account

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"time"
)

type HistoryDeletion struct {
	Before    time.Time `json:"before"`
	Signature string    `json:"signature"`
}

func (s *Store) deletionMessage(d HistoryDeletion) []byte {
	d.Signature = ""
	b, _ := json.Marshal(d)
	return append([]byte("meshway-history-deletion-v1\x00"+rootID(s.root.Public().(ed25519.PublicKey))+"\x00"), b...)
}
func (s *Store) verifyDeletion(d HistoryDeletion) error {
	signature, err := decode(d.Signature)
	if err != nil || d.Before.IsZero() || d.Before.After(time.Now().Add(5*time.Minute)) || !ed25519.Verify(s.root.Public().(ed25519.PublicKey), s.deletionMessage(d), signature) {
		return errors.New("invalid signed history deletion")
	}
	return nil
}

// ClearHistory optionally signs a deletion cutoff so older sync snapshots cannot
// restore deleted events. Local-only clearing affects just this device.
func (s *Store) ClearHistory(synchronize bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("account is closed")
	}
	previous := s.data
	s.data.Events = []Event{}
	if synchronize {
		d := HistoryDeletion{Before: time.Now().UTC()}
		d.Signature = encode(ed25519.Sign(s.root, s.deletionMessage(d)))
		s.data.Deletion = &d
	}
	if err := s.save(); err != nil {
		s.data = previous
		return err
	}
	return nil
}
