package util

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var RetryStopErr = errors.New("no to retry")

/**
重试
*/
func Retry(f func() error, times int, space time.Duration) error {
	if times == 0 || times == 1 {
		return f()
	}
	var err error
	var i = 0
	for {
		err = f()
		if err == nil {
			return nil
		}
		if errors.Is(err, RetryStopErr) {
			return err
		}
		i++
		if times > 1 && i >= times {
			break
		}
		time.Sleep(space)
	}
	return err
}

func NewRetryStopErr(msg string) error {
	return fmt.Errorf(msg, RetryStopErr)
}

/**
无限重试
*/
func RetryCancelWithContext(f func() error, times int,space time.Duration,ctx context.Context) error {
	if times == 0 || times == 1 {
		return f()
	}
	var err error
	var i = 0
	for {
		select {
		case <-ctx.Done():
			return err
		default:
		}
		err = f()
		if err == nil {
			return nil
		}
		i++
		if times > 1 && i >= times {
			break
		}
		time.Sleep(space)
	}
	return  err
}
