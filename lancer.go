package lancer // import "github.com/ei-grad/lancer"

import (
	"context"
	"errors"
	"math"
	"time"
)

var (
	ErrNoReceiver  = errors.New("no ready receiver on channel")
	ErrTickMissed  = errors.New("tick time has been missed")
	ErrBadDuration = errors.New("duration is not enought to send any requests")
)

// Linear generates ticks with linearly increasing count of ticks per second
// from low to high during specified duration. Ticks are sent to the lance
// channel. Use ctx to stop the tick generation if needed.
func Linear(
	ctx context.Context,
	lance chan time.Duration,
	low float64,
	high float64,
	duration time.Duration,
) error {

	if duration <= 0 {
		return ErrBadDuration
	}

	//if ctx == nil {
	//	ctx = context.Background()
	//}

	var (
		durationSeconds = float64(duration) / float64(time.Second)
		lowSq           = low * low
		slope           = (high - low) / durationSeconds
	)

	tickTime := func(n int) time.Duration {
		if slope == 0 {
			return time.Duration(float64(n*int(time.Second)) / low)
		}
		ret := (math.Sqrt(lowSq+2*slope*float64(n)) - low) / slope
		return time.Duration(ret * float64(time.Second))
	}

	if tickTime(1) > duration {
		return ErrBadDuration
	}

	count := int((high + low) * durationSeconds / 2)
	start := time.Now()

	for i := 1; i < count+1; i++ {
		nextTick := tickTime(i)
		dt := start.Add(nextTick).Sub(time.Now())
		//if dt < 0 {
		//	return ErrTickMissed
		//}
		if dt > time.Millisecond {
			time.Sleep(dt)
		}
		select {
		case <-ctx.Done():
			return context.Canceled
		case lance <- nextTick:
		default:
			return ErrNoReceiver
		}
	}

	return nil
}
