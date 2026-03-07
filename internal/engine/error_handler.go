package engine

import (
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog"
)

// ErrorClass classifies an error for the retry/DLQ decision.
type ErrorClass uint8

const (
	ErrorClassTransient ErrorClass = iota // Retryable.
	ErrorClassPoison                      // Route to DLQ.
	ErrorClassFatal                       // Fail the job immediately.
)

// ExhaustedAction determines what happens when retries are exhausted.
// FailJob is iota=0 so the zero-value preserves legacy behavior.
type ExhaustedAction uint8

const (
	FailJob    ExhaustedAction = iota // Default: fail the pipeline.
	RouteToDLQ                        // Send to DLQ.
	DropEvent                         // Log and drop.
)

// BackoffStrategy computes the delay before the given retry attempt (0-indexed).
type BackoffStrategy func(attempt int) time.Duration

// ErrorClassifier classifies an error into an ErrorClass.
type ErrorClassifier func(err error) ErrorClass

// ErrorHandlerConfig holds per-operator error handling configuration.
type ErrorHandlerConfig struct {
	OperatorName string          // Human-readable operator name (for metrics/DLQ).
	MaxRetries   int             // 0 = no retries (default).
	Backoff      BackoffStrategy // nil = no backoff.
	OnExhausted  ExhaustedAction // FailJob (zero) = legacy behavior.
	Classifier   ErrorClassifier // nil = use defaultClassifier.
}

// ChainLink pairs an Operator with its ErrorHandlerConfig.
type ChainLink struct {
	Operator Operator
	Config   ErrorHandlerConfig
}

// FixedBackoff returns a BackoffStrategy that always waits the given delay.
func FixedBackoff(delay time.Duration) BackoffStrategy {
	return func(_ int) time.Duration {
		return delay
	}
}

// ExponentialBackoff returns a BackoffStrategy with exponential delay,
// capped at maxDelay.
func ExponentialBackoff(initialDelay, maxDelay time.Duration, multiplier float64) BackoffStrategy {
	return func(attempt int) time.Duration {
		d := float64(initialDelay)
		for i := 0; i < attempt; i++ {
			d *= multiplier
		}
		if time.Duration(d) > maxDelay {
			return maxDelay
		}
		return time.Duration(d)
	}
}

// defaultClassifier classifies errors based on sentinel wrapping.
func defaultClassifier(err error) ErrorClass {
	if errors.Is(err, ErrFatal) {
		return ErrorClassFatal
	}
	if errors.Is(err, ErrTransient) {
		return ErrorClassTransient
	}
	return ErrorClassPoison
}

// classify returns the ErrorClass for err using the config's classifier
// or the default classifier.
func classify(cfg ErrorHandlerConfig, err error) ErrorClass {
	if cfg.Classifier != nil {
		return cfg.Classifier(err)
	}
	return defaultClassifier(err)
}

// safeInvoke calls fn with panic recovery. Panics are wrapped as ErrOperatorPanic.
func safeInvoke(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", ErrOperatorPanic, r)
		}
	}()
	return fn()
}

// invokeWithRetry executes fn with retry/DLQ/fail logic according to the
// ChainLink's config. Returns nil if the event was handled (success, DLQ'd,
// or dropped) or an error if the job should fail.
func invokeWithRetry(
	cc *chainContext,
	link ChainLink,
	event Event,
	fn func() error,
) error {
	cfg := link.Config
	metrics := cc.errMetrics

	err := safeInvoke(fn)
	if err == nil {
		return nil
	}

	metrics.IncErrorTotal(cfg.OperatorName)

	cls := classify(cfg, err)

	// Fatal errors always fail the job immediately.
	if cls == ErrorClassFatal {
		return err
	}

	// Poison errors skip retries and go straight to exhausted handling.
	if cls == ErrorClassPoison {
		return handleExhausted(cfg, event, err, 0, cc.dlqCh, metrics, cc.log)
	}

	// Transient errors: retry up to MaxRetries times.
	for attempt := 1; attempt <= cfg.MaxRetries; attempt++ {
		metrics.IncRetryTotal(cfg.OperatorName)

		// Apply backoff with context cancellation support.
		if cfg.Backoff != nil {
			delay := cfg.Backoff(attempt - 1)
			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-cc.ctx.Done():
					return cc.ctx.Err()
				}
			}
		}

		err = safeInvoke(fn)
		if err == nil {
			return nil
		}

		metrics.IncErrorTotal(cfg.OperatorName)

		// Re-classify — error nature may change between attempts.
		cls = classify(cfg, err)
		if cls == ErrorClassFatal {
			return err
		}
		if cls == ErrorClassPoison {
			return handleExhausted(cfg, event, err, attempt, cc.dlqCh, metrics, cc.log)
		}
	}

	// Retries exhausted.
	return handleExhausted(cfg, event, fmt.Errorf("%w: %v", ErrRetriesExhausted, err), cfg.MaxRetries, cc.dlqCh, metrics, cc.log)
}

// handleExhausted applies the OnExhausted policy. Returns nil for DLQ/Drop
// (event handled), or the error for FailJob.
func handleExhausted(
	cfg ErrorHandlerConfig,
	event Event,
	err error,
	retryCount int,
	dlqCh chan<- DLQEvent,
	metrics ErrorMetrics,
	log zerolog.Logger,
) error {
	switch cfg.OnExhausted {
	case RouteToDLQ:
		dlqEvent := DLQEvent{
			OriginalEvent: event,
			Error:         err.Error(),
			OperatorName:  cfg.OperatorName,
			Timestamp:     time.Now().UnixMilli(),
			RetryCount:    retryCount,
		}
		if dlqCh != nil {
			select {
			case dlqCh <- dlqEvent:
				metrics.IncDLQTotal(cfg.OperatorName)
			default:
				metrics.IncDLQOverflowTotal(cfg.OperatorName)
				log.Error().Str("operator", cfg.OperatorName).Msg("DLQ channel full, dropping DLQ event")
			}
		}
		return nil
	case DropEvent:
		metrics.IncDropTotal(cfg.OperatorName)
		log.Warn().Str("operator", cfg.OperatorName).Err(err).Msg("dropping event after exhausted retries")
		return nil
	default: // FailJob
		return err
	}
}

// buildChainLinks pairs operators with their error configs. Missing configs
// get zero-value ErrorHandlerConfig (legacy behavior: fail on any error).
func buildChainLinks(operators []Operator, errorConfigs []ErrorHandlerConfig) []ChainLink {
	links := make([]ChainLink, len(operators))
	for i, op := range operators {
		var cfg ErrorHandlerConfig
		if i < len(errorConfigs) {
			cfg = errorConfigs[i]
		}
		links[i] = ChainLink{Operator: op, Config: cfg}
	}
	return links
}
