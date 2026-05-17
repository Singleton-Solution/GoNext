package pool

import (
	"context"
	"time"
)

// reaperLoop is the background goroutine that:
//
//  1. Closes instances that have sat idle longer than MaxIdleTime,
//     subject to the MinInstances floor.
//  2. Re-creates instances when the live count is below
//     MinInstances (typically after a trap recycle on Return).
//
// One reaper per Pool, started by NewPool. Exits when reaperStop is
// closed by Pool.Close. The goroutine signals reaperDone on the way
// out so Close can wait for it before tearing the idle slice down.
//
// The reaper uses time.NewTicker so the interval is deterministic.
// Tests that want to make the reaper observable on a fine time
// scale pass a small ReapInterval (e.g. 5 ms) in the Config.
func (p *Pool) reaperLoop() {
	defer close(p.reaperDone)

	t := time.NewTicker(p.cfg.ReapInterval)
	defer t.Stop()

	for {
		select {
		case <-p.reaperStop:
			return
		case <-t.C:
			p.reapOnce()
		}
	}
}

// reapOnce performs one reaper pass. Exported (to package tests) via
// a separate helper rather than a knob on Pool itself — keeps the
// public surface small.
func (p *Pool) reapOnce() {
	if p.closed.Load() {
		return
	}

	now := p.cfg.now()

	// Eviction phase. Walk the idle slice and collect instances
	// older than MaxIdleTime, subject to the MinInstances floor.
	//
	// Because the idle slice is LIFO (newest at the back), the
	// oldest instances are at the front. Cheaper would be to keep
	// the slice as a doubly-linked list, but the slice's
	// O(n) copy cost is negligible at MaxInstances ≤ 64 — the
	// realistic upper bound for plugin pools.
	p.mu.Lock()
	toClose := make([]*instance, 0)
	if p.cfg.MaxIdleTime > 0 {
		// Compute how many we may remove: live - min, but never
		// fewer than 0. Each eviction decrements live, so the
		// budget here is the running tally.
		budget := p.live - p.cfg.MinInstances
		if budget < 0 {
			budget = 0
		}
		remaining := make([]*instance, 0, len(p.idle))
		for _, inst := range p.idle {
			if budget > 0 && now.Sub(inst.lastUsed) >= p.cfg.MaxIdleTime {
				toClose = append(toClose, inst)
				budget--
				continue
			}
			remaining = append(remaining, inst)
		}
		p.idle = remaining
		p.live -= len(toClose)
	}

	// Refill phase. If live < MinInstances (typically because a
	// trap recycle on Return knocked us below), we want to
	// reclaim the floor. Compute the need under the lock; release
	// it before LoadModule because instantiation is slow (1–5 ms)
	// and we'd otherwise block Checkout.
	need := p.cfg.MinInstances - p.live
	if need < 0 {
		need = 0
	}
	// Reserve the live slots up front, so a concurrent Checkout
	// can't squeeze in past MaxInstances during the brief window
	// we release the lock.
	if need > 0 {
		p.live += need
	}
	p.mu.Unlock()

	// Close evicted instances outside the lock. wazero close is
	// fast (sub-millisecond) but we still don't want to hold the
	// pool mutex across cgo if at all avoidable.
	for _, inst := range toClose {
		_ = inst.close(context.Background())
		p.observeRecycle(RecycleReasonIdle)
	}
	if len(toClose) > 0 {
		p.observePoolSize()
	}

	// Refill. Create-and-push, one at a time. On error we just
	// give back the slot — the next reap tick will try again.
	if need > 0 {
		created := make([]*instance, 0, need)
		for i := 0; i < need; i++ {
			if p.closed.Load() {
				break
			}
			inst, err := p.createInstance(context.Background())
			if err != nil {
				// Give back the unused reservation.
				p.mu.Lock()
				p.live -= (need - i)
				p.mu.Unlock()
				break
			}
			inst.markReturned(p.cfg.now())
			created = append(created, inst)
		}
		if len(created) > 0 {
			p.mu.Lock()
			p.idle = append(p.idle, created...)
			// Wake any Checkout that was blocked because live ==
			// max (it won't be — but Broadcast is cheap and
			// covers the trap-then-refill race).
			p.cond.Broadcast()
			p.mu.Unlock()
			p.observePoolSize()
		}
	}
}
