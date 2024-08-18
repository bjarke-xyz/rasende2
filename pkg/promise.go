package pkg

import (
	"log"
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
		log.Printf("Promise f %T: result=%+v, err=%v", f, result, err)
		if err != nil {
			log.Printf("Promise f %T sending %v to err", f, err)
			promise.err = err
		} else {
			log.Printf("Promise f %T sending %v to result", f, result)
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
