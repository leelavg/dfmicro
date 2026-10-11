package support

import (
	"cmp"
	"context"
	"errors"
	"time"
)

// Poll repeats check until it reports ready or the timeout expires.
func Poll(ctx context.Context, interval, timeout time.Duration, check func(context.Context) (bool, error)) error {
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		if err := pollCtx.Err(); err != nil {
			return err
		}
		ready, err := check(pollCtx)
		if err = cmp.Or(pollCtx.Err(), err); err != nil {
			return err
		} else if ready {
			return nil
		}
		if err := wait(pollCtx, interval); err != nil {
			return err
		}
	}
}

// Retry repeats attempt after retryable errors, until it succeeds or attempts are exhausted.
func Retry(ctx context.Context, attempts int, interval time.Duration, retryable func(error) bool, attempt func(context.Context) error) error {
	if attempts <= 0 {
		return errors.New("retry attempts must be positive")
	}
	for number := range attempts {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := attempt(ctx)
		if err == nil || !retryable(err) {
			return err
		}
		if number == attempts-1 {
			return err
		}
		if err := wait(ctx, interval); err != nil {
			return err
		}
	}
	return nil
}

func wait(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
