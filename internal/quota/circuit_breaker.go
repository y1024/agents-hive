package quota

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrDepthExceeded = errors.New("delegation depth exceeded")
	ErrTotalExceeded = errors.New("delegation total exceeded")
	ErrTimeout       = errors.New("delegation acquire timeout")
)

type Limits struct {
	MaxDepth       int
	MaxConcurrent  int
	MaxTotal       int
	AcquireTimeout time.Duration
}

type Request struct {
	Depth int
}

type CircuitBreaker struct {
	limits Limits
	sem    chan struct{}

	mu       sync.Mutex
	total    int
	inFlight int
}

type Lease struct {
	once    sync.Once
	release func()
}

func NewCircuitBreaker(limits Limits) *CircuitBreaker {
	maxConcurrent := limits.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	return &CircuitBreaker{
		limits: limits,
		sem:    make(chan struct{}, maxConcurrent),
	}
}

func (b *CircuitBreaker) Acquire(ctx context.Context, req Request) (*Lease, error) {
	if b.limits.MaxDepth > 0 && req.Depth > b.limits.MaxDepth {
		return nil, ErrDepthExceeded
	}

	if err := b.reserveTotal(); err != nil {
		return nil, err
	}

	if err := b.acquireSlot(ctx); err != nil {
		b.unreserveTotal()
		return nil, err
	}

	b.mu.Lock()
	b.inFlight++
	b.mu.Unlock()

	return &Lease{release: func() {
		<-b.sem
		b.mu.Lock()
		b.inFlight--
		b.mu.Unlock()
	}}, nil
}

func (b *CircuitBreaker) reserveTotal() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limits.MaxTotal > 0 && b.total >= b.limits.MaxTotal {
		return ErrTotalExceeded
	}
	b.total++
	return nil
}

func (b *CircuitBreaker) unreserveTotal() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.total > 0 {
		b.total--
	}
}

func (b *CircuitBreaker) acquireSlot(ctx context.Context) error {
	acquireCtx := ctx
	cancel := func() {}
	if b.limits.AcquireTimeout > 0 {
		acquireCtx, cancel = context.WithTimeout(ctx, b.limits.AcquireTimeout)
	}
	defer cancel()

	select {
	case b.sem <- struct{}{}:
		return nil
	case <-acquireCtx.Done():
		if errors.Is(acquireCtx.Err(), context.DeadlineExceeded) {
			return ErrTimeout
		}
		return acquireCtx.Err()
	}
}

func (l *Lease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		if l.release != nil {
			l.release()
		}
	})
}

func (b *CircuitBreaker) InFlight() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inFlight
}
