package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"syscall"
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
	// SanitizeDiagnostic filters diagnostic text before it enters a DLQ.
	// It does not alter original records or error classification.
	SanitizeDiagnostic func(string) string
	// DLQWriter synchronously delivers a failed event; failures are logged and counted as drops.
	DLQWriter    func(context.Context, DLQEvent) error
	OperatorName string          // Human-readable operator name (for metrics/DLQ).
	MaxRetries   int             // 0 = no retries (default).
	Backoff      BackoffStrategy // nil = no backoff.
	OnExhausted  ExhaustedAction // FailJob (zero) = legacy behavior.
	Classifier   ErrorClassifier // nil = use defaultClassifier.
}

// ChainLink pairs an Operator with its ErrorHandlerConfig.
type ChainLink struct {
	batch    *sinkBatch
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
		if initialDelay <= 0 || maxDelay <= 0 {
			return 0
		}
		if attempt < 0 {
			attempt = 0
		}
		if math.IsNaN(multiplier) || multiplier < 1 {
			multiplier = 1
		}
		d := float64(initialDelay) * math.Pow(multiplier, float64(attempt))
		// Compare before converting: an overflowing float-to-duration cast
		// can become negative and accidentally disable the retry delay.
		if math.IsInf(d, 1) || d >= float64(maxDelay) {
			return maxDelay
		}
		return time.Duration(d)
	}
}

// defaultClassifier recognizes explicit markers and standard resource/network
// failures. Domain-specific errors can wrap ErrTransient or ErrFatal.
func defaultClassifier(err error) ErrorClass {
	if errors.Is(err, ErrFatal) || errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.ENOMEM) {
		return ErrorClassFatal
	}
	if errors.Is(err, ErrTransient) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE) {
		return ErrorClassTransient
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return ErrorClassTransient
	}
	return ErrorClassPoison
}

// classify returns the ErrorClass for err using the config's classifier
// or the default classifier.
func classify(cfg ErrorHandlerConfig, err error) ErrorClass {
	classified := defaultClassifier(err)
	if classified == ErrorClassFatal {
		return classified
	}
	if cfg.Classifier != nil {
		custom := cfg.Classifier(err)
		if custom > ErrorClassFatal {
			return ErrorClassFatal
		}
		return custom
	}
	return classified
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

	if err := cc.ctx.Err(); err != nil {
		return err
	}
	err := safeInvoke(fn)
	if err == nil {
		return nil
	}

	// Task cancellation is a lifecycle event, not a poison record.
	if ctxErr := cc.ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	cls := classify(cfg, err)
	recordOperatorError(metrics, cfg.OperatorName, cls)

	// Fatal errors always fail the job immediately.
	if cls == ErrorClassFatal {
		return err
	}

	// Poison errors skip retries and go straight to exhausted handling.
	if cls == ErrorClassPoison {
		return handleExhausted(cc.ctx, cfg, event, err, 0, cc.dlqCh, metrics, cc.log)
	}

	// Transient errors: retry up to MaxRetries times.
	for attempt := 1; attempt <= cfg.MaxRetries; attempt++ {
		if err := cc.ctx.Err(); err != nil {
			return err
		}

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

		if err := cc.ctx.Err(); err != nil {
			return err
		}
		metrics.IncRetryTotal(cfg.OperatorName)
		err = safeInvoke(fn)
		if err == nil {
			return nil
		}

		// Task cancellation is a lifecycle event, not a poison record.
		if ctxErr := cc.ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		// Re-classify — error nature may change between attempts.
		cls = classify(cfg, err)
		recordOperatorError(metrics, cfg.OperatorName, cls)
		if cls == ErrorClassFatal {
			return err
		}
		if cls == ErrorClassPoison {
			return handleExhausted(cc.ctx, cfg, event, err, attempt, cc.dlqCh, metrics, cc.log)
		}
	}

	// Retries exhausted.
	return handleExhausted(cc.ctx, cfg, event, fmt.Errorf("%w: %w", ErrRetriesExhausted, err), cfg.MaxRetries, cc.dlqCh, metrics, cc.log)
}

// handleExhausted applies the OnExhausted policy. Returns nil for DLQ/Drop
// (event handled), or the error for FailJob.
func handleExhausted(
	ctx context.Context,
	cfg ErrorHandlerConfig,
	event Event,
	err error,
	retryCount int,
	dlqCh chan<- DLQEvent,
	metrics ErrorMetrics,
	log zerolog.Logger,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch cfg.OnExhausted {
	case RouteToDLQ:
		dlqEvent := DLQEvent{
			OriginalEvent: event,
			Error:         err.Error(),
			OperatorName:  cfg.OperatorName,
			Timestamp:     time.Now().UnixMilli(),
			RetryCount:    retryCount,
		}
		if cfg.SanitizeDiagnostic != nil {
			dlqEvent.Error = cfg.SanitizeDiagnostic(dlqEvent.Error)
		}
		if cfg.DLQWriter != nil {
			if writeErr := safeInvoke(func() error { return cfg.DLQWriter(ctx, dlqEvent) }); writeErr != nil {
				if err := ctx.Err(); err != nil {
					return err
				}
				metrics.IncDropTotal(cfg.OperatorName)
				log.Error().Str("operator", cfg.OperatorName).Err(writeErr).Msg("DLQ sink failed, dropping event")
			} else {
				metrics.IncDLQTotal(cfg.OperatorName)
			}
			return nil
		}
		if dlqCh != nil {
			select {
			case dlqCh <- dlqEvent:
				metrics.IncDLQTotal(cfg.OperatorName)
			default:
				metrics.IncDLQOverflowTotal(cfg.OperatorName)
				metrics.IncDropTotal(cfg.OperatorName)
				log.Error().Str("operator", cfg.OperatorName).Msg("DLQ channel full, dropping DLQ event")
			}
		} else {
			metrics.IncDropTotal(cfg.OperatorName)
			log.Error().Str("operator", cfg.OperatorName).Msg("DLQ not configured, dropping event")
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

// ValidateTransactionalErrorPolicies rejects record-level recovery on a sink
// that may have staged a write before returning an error. Replaying the whole
// transaction is necessary; retrying or dropping one record is unsafe.
func ValidateTransactionalErrorPolicies(operators []Operator, configs []ErrorHandlerConfig) error {
	for i, op := range operators {
		if _, ok := op.(TransactionalSink); !ok || i >= len(configs) {
			continue
		}
		cfg := configs[i]
		if cfg.MaxRetries != 0 || cfg.OnExhausted != FailJob {
			return fmt.Errorf("transactional sink %q requires fail policy with no record retries", cfg.OperatorName)
		}
	}
	return nil
}
