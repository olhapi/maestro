package kanban

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNotFound          = errors.New("kanban: not found")
	ErrValidation        = errors.New("kanban: validation error")
	ErrInvalidState      = errors.New("kanban: invalid state")
	ErrBlockedTransition = errors.New("kanban: blocked transition")
)

func notFoundError(entity, id string) error {
	if id == "" {
		return fmt.Errorf("%w: %s", ErrNotFound, entity)
	}
	return fmt.Errorf("%w: %s %q", ErrNotFound, entity, id)
}

func validationErrorf(format string, args ...interface{}) error {
	return fmt.Errorf("%w: %s", ErrValidation, fmt.Sprintf(format, args...))
}

func invalidStateError(state State) error {
	return fmt.Errorf("%w: %s", ErrInvalidState, state)
}

func invalidPhaseError(phase WorkflowPhase) error {
	return fmt.Errorf("%w: invalid workflow phase %q", ErrValidation, phase)
}

func blockedInProgressError(blockers []string) error {
	if len(blockers) == 1 {
		return fmt.Errorf("%w: cannot move issue to in_progress: blocked by %s. Move the blocker to done or cancelled, or remove it from blocked_by first", ErrBlockedTransition, blockers[0])
	}
	return fmt.Errorf("%w: cannot move issue to in_progress: blocked by %s. Move those blockers to done or cancelled, or remove them from blocked_by first", ErrBlockedTransition, strings.Join(blockers, ", "))
}

func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows)
}

func IsValidation(err error) bool {
	return errors.Is(err, ErrValidation) || errors.Is(err, ErrInvalidState) || errors.Is(err, ErrBlockedTransition)
}

func IsBlockedTransition(err error) bool {
	return errors.Is(err, ErrBlockedTransition)
}
