package pkg

import (
	"sync"
)

type Promise[T any] struct {
	result T
	err    error
	wg     sync.WaitGroup
	done   bool
}

func NewPromise[T any](f func() (T, error)) *Promise[T] {
	var zero T
	promise := &Promise[T]{
		result: zero,
		err:    nil,
	}
	promise.wg.Add(1)
	go func() {
		result, err := f()
		if err != nil {
			promise.err = err
		} else {
			promise.result = result
		}
		promise.done = true
		promise.wg.Done()
	}()
	return promise
}

func (p *Promise[T]) Poll() (T, error, bool) {
	return p.result, p.err, p.done
}

func (p *Promise[T]) Get() (T, error) {
	p.wg.Wait()
	return p.result, p.err
}
