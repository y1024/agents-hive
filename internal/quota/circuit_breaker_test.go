package quota

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCircuitBreakerRejectsDepthAndTotalLimits(t *testing.T) {
	breaker := NewCircuitBreaker(Limits{MaxDepth: 2, MaxTotal: 1, MaxConcurrent: 1})

	if _, err := breaker.Acquire(context.Background(), Request{Depth: 3}); !errors.Is(err, ErrDepthExceeded) {
		t.Fatalf("depth err = %v, want ErrDepthExceeded", err)
	}

	lease, err := breaker.Acquire(context.Background(), Request{Depth: 1})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	lease.Release()

	if _, err := breaker.Acquire(context.Background(), Request{Depth: 1}); !errors.Is(err, ErrTotalExceeded) {
		t.Fatalf("total err = %v, want ErrTotalExceeded", err)
	}
}

func TestCircuitBreakerConcurrentLimitRaceSafeAndReleaseIdempotent(t *testing.T) {
	breaker := NewCircuitBreaker(Limits{MaxDepth: 10, MaxTotal: 64, MaxConcurrent: 5})
	start := make(chan struct{})
	var active int32
	var peak int32
	var wg sync.WaitGroup

	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			lease, err := breaker.Acquire(context.Background(), Request{Depth: 1})
			if err != nil {
				return
			}
			current := atomic.AddInt32(&active, 1)
			for {
				observed := atomic.LoadInt32(&peak)
				if current <= observed || atomic.CompareAndSwapInt32(&peak, observed, current) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			atomic.AddInt32(&active, -1)
			lease.Release()
			lease.Release()
		}()
	}

	close(start)
	wg.Wait()

	if peak > 5 {
		t.Fatalf("peak concurrency = %d, want <= 5", peak)
	}
	if breaker.InFlight() != 0 {
		t.Fatalf("in flight = %d, want 0", breaker.InFlight())
	}
}

func TestCircuitBreakerAcquireHonorsTimeout(t *testing.T) {
	breaker := NewCircuitBreaker(Limits{MaxDepth: 10, MaxTotal: 10, MaxConcurrent: 1, AcquireTimeout: time.Millisecond})
	lease, err := breaker.Acquire(context.Background(), Request{Depth: 1})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer lease.Release()

	if _, err := breaker.Acquire(context.Background(), Request{Depth: 1}); !errors.Is(err, ErrTimeout) {
		t.Fatalf("timeout err = %v, want ErrTimeout", err)
	}
}
