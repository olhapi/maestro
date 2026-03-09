package kanban

import (
	"database/sql"
	"errors"
	"fmt"
)

var (
	ErrNotFound     = errors.New("kanban: not found")
	ErrValidation   = errors.New("kanban: validation error")
	ErrInvalidState = errors.New("kanban: invalid state")
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

func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows)
}

func IsValidation(err error) bool {
	return errors.Is(err, ErrValidation) || errors.Is(err, ErrInvalidState)
}
