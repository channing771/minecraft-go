package sim

import (
	"context"
	"log/slog"
	"time"
)

func (engine *Engine) Run(ctx context.Context, clock Clock) error {
	if clock == nil {
		clock = newTickerClock(productionTickInterval)
	}
	defer clock.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case tickTime, ok := <-clock.C():
			if !ok {
				return nil
			}
			steps := 1
			missed := int(time.Since(tickTime) / productionTickInterval)
			if missed > 0 {
				steps += min(missed, maxCatchUpSteps-1)
			}
			if missed >= maxCatchUpSteps {
				slog.Warn(
					"权威 tick 落后，限制追赶并重新定基准",
					"missed_ticks",
					missed,
				)
			}
			for range steps {
				engine.Step()
			}
		}
	}
}

type tickerClock struct {
	ticker *time.Ticker
}

func newTickerClock(interval time.Duration) *tickerClock {
	return &tickerClock{ticker: time.NewTicker(interval)}
}

func (clock *tickerClock) C() <-chan time.Time {
	return clock.ticker.C
}

func (clock *tickerClock) Stop() {
	clock.ticker.Stop()
}
