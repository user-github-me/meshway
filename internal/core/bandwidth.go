package core

import (
	"context"
	"io"

	"github.com/libp2p/go-libp2p/core/peer"
	"golang.org/x/time/rate"
)

func (n *Node) bandwidth(id peer.ID) *rate.Limiter {
	if n.Config.Limits == nil || n.Config.Limits.BytesPerSecond == 0 {
		return nil
	}
	n.bandwidthMu.Lock()
	defer n.bandwidthMu.Unlock()
	limiter := n.bandwidthLimits[id]
	if limiter == nil {
		// The cache is bounded; disconnected peers are removed before admitting
		// new ones. Refusing overflow avoids unlimited unauthenticated key growth.
		if len(n.bandwidthLimits) >= 2048 {
			for p := range n.bandwidthLimits {
				if !n.peerConnected(p) {
					delete(n.bandwidthLimits, p)
				}
			}
		}
		if len(n.bandwidthLimits) >= 2048 {
			return rate.NewLimiter(1, 32768)
		}
		limiter = rate.NewLimiter(rate.Limit(n.Config.Limits.BytesPerSecond), max(32768, n.Config.Limits.BytesPerSecond))
		n.bandwidthLimits[id] = limiter
	}
	return limiter
}

func waitBandwidth(ctx context.Context, limiter *rate.Limiter, size int) error {
	if limiter == nil {
		return nil
	}
	for size > 0 {
		chunk := min(size, limiter.Burst())
		if err := limiter.WaitN(ctx, chunk); err != nil {
			return err
		}
		size -= chunk
	}
	return nil
}

type bandwidthWriter struct {
	io.Writer
	ctx     context.Context
	limiter *rate.Limiter
}

func (w bandwidthWriter) Write(data []byte) (int, error) {
	if err := waitBandwidth(w.ctx, w.limiter, len(data)); err != nil {
		return 0, err
	}
	return w.Writer.Write(data)
}
