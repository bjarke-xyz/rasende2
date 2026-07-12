package pkg

import (
	"sync"
)

// Promise runs f in the background. Get blocks for the result; Poll reports it
// only once it is there, so a caller with other work to do — streaming an
// article while its image is still being generated — can check in without
// stalling.
//
// The mutex is what makes Poll legal. Get alone could rely on the WaitGroup to
// order the write before the read, but Poll by definition reads while f may
// still be running, and an unsynchronised read of done would be a data race:
// the poller is entitled to never observe the result at all.
type Promise[T any] struct {
	mu     sync.Mutex
	result T
	err    error
	done   bool
	wg     sync.WaitGroup
}

func NewPromise[T any](f func() (T, error)) *Promise[T] {
	p := &Promise[T]{}
	p.wg.Go(func() {
		result, err := f()
		p.mu.Lock()
		defer p.mu.Unlock()
		p.result, p.err, p.done = result, err, true
	})
	return p
}

// Poll returns the result and whether f has finished. Until it has, the result
// and error are zero.
func (p *Promise[T]) Poll() (T, error, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.result, p.err, p.done
}

func (p *Promise[T]) Get() (T, error) {
	p.wg.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.result, p.err
}
