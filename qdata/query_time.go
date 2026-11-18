package qdata

import (
	"errors"
	"fmt"
	"slices"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var errInvalidQueryTime = errors.New("invalid query time context")

// ValidateQueryTime checks transport-independent context, bounds, and step values.
func ValidateQueryTime(query *Query) error {
	switch query.GetContext() {
	case ContextInstant, ContextRange, ContextStreaming:
	default:
		return fmt.Errorf("%w: unknown context", errInvalidQueryTime)
	}

	window := query.GetRange()

	if query.GetContext() == ContextRange {
		if window.GetStart() == nil || window.GetEnd() == nil {
			return fmt.Errorf("%w: range requires start and end", errInvalidQueryTime)
		}

		metrics := query.GetSignal() == SignalMetrics || slices.Contains(PlanSignals(query.GetPlan()), SignalMetrics)
		if metrics && query.GetStep() == nil {
			return fmt.Errorf("%w: metrics range requires step", errInvalidQueryTime)
		}
	}

	err := validateTimeBounds(window)
	if err != nil {
		return err
	}

	return validateQueryStep(query.GetStep())
}

func validateTimeBounds(window *TimeRange) error {
	for _, stamp := range []*timestamppb.Timestamp{window.GetStart(), window.GetEnd()} {
		if stamp != nil {
			err := stamp.CheckValid()
			if err != nil {
				return fmt.Errorf("%w: timestamp: %w", errInvalidQueryTime, err)
			}
		}
	}

	if window.GetStart() != nil && window.GetEnd() != nil && window.GetStart().AsTime().After(window.GetEnd().AsTime()) {
		return fmt.Errorf("%w: start exceeds end", errInvalidQueryTime)
	}

	return nil
}

func validateQueryStep(step *durationpb.Duration) error {
	if step != nil {
		err := step.CheckValid()
		if err != nil {
			return fmt.Errorf("%w: step: %w", errInvalidQueryTime, err)
		}

		duration := step.AsDuration()

		encoded := durationpb.New(duration)
		if duration <= 0 || encoded.GetSeconds() != step.GetSeconds() || encoded.GetNanos() != step.GetNanos() {
			return fmt.Errorf("%w: step must be positive and fit a Go duration", errInvalidQueryTime)
		}
	}

	return nil
}
