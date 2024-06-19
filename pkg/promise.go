package pkg

type Promise[T any] struct {
	result chan T
	err    chan error
}

func NewPromise[T any](f func() (T, error)) *Promise[T] {
	promise := &Promise[T]{
		result: make(chan T, 1),
		err:    make(chan error, 1),
	}
	go func() {
		defer close(promise.err)
		defer close(promise.result)
		result, err := f()
		if err != nil {
			promise.err <- err
		} else {
			promise.result <- result
		}
	}()
	return promise
}

func (p *Promise[T]) Get() (T, error) {
	select {
	case err := <-p.err:
		var zero T // Zero value of T
		return zero, err
	case result := <-p.result:
		return result, nil
	}
}
