package grpc

import "time"

// DefaultBackoffConfig uses values specified for backoff in
// https://github.com/grpc/grpc/blob/master/doc/connection-backoff.md.
var (
	DefaultBackoffConfig = BackoffConfig{
		MaxDelay:  120 * time.Second,
		baseDelay: 1.0 * time.Second,
		factor:    1.6,
		jitter:    0.2,
	}
)

// BackoffConfig defines the parameters for the default gRPC backoff strategy.
type BackoffConfig struct {
	// MaxDelay is the upper bound of backoff delay.
	MaxDelay time.Duration

	// TODO(stevvooe): The following fields are not exported, as allowing
	// changes would violate the current gRPC specification for backoff. If
	// gRPC decides to allow more interesting backoff strategies, these fields
	// may be opened up in the future.

	// baseDelay is the amount of time to wait before retrying after the first
	// failure.
	baseDelay time.Duration

	// factor is applied to the backoff after each retry.
	factor float64

	// jitter provides a range to randomize backoff delays.
	jitter float64
}
