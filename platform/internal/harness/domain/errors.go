package domain

import (
	"errors"
	"fmt"
)

type ErrorCode string

const (
	CodeInvalidArgument   ErrorCode = "HARNESS_INVALID_ARGUMENT"
	CodeInvalidState      ErrorCode = "HARNESS_INVALID_STATE"
	CodeConflict          ErrorCode = "HARNESS_CONFLICT"
	CodeExpired           ErrorCode = "HARNESS_EXPIRED"
	CodeRevoked           ErrorCode = "HARNESS_REVOKED"
	CodeNotFound          ErrorCode = "HARNESS_NOT_FOUND"
	CodeResourceLimit     ErrorCode = "HARNESS_RESOURCE_LIMIT"
	CodeSecurityViolation ErrorCode = "HARNESS_SECURITY_VIOLATION"
)

type Problem struct {
	Code    ErrorCode
	Message string
	Cause   error
}

func (problem *Problem) Error() string {
	if problem == nil {
		return "<nil>"
	}
	if problem.Cause == nil {
		return fmt.Sprintf("%s: %s", problem.Code, problem.Message)
	}
	return fmt.Sprintf("%s: %s: %v", problem.Code, problem.Message, problem.Cause)
}

func (problem *Problem) Unwrap() error {
	if problem == nil {
		return nil
	}
	return problem.Cause
}

func NewProblem(code ErrorCode, message string, cause error) error {
	return &Problem{Code: code, Message: message, Cause: cause}
}

func HasCode(err error, code ErrorCode) bool {
	var problem *Problem
	return errors.As(err, &problem) && problem.Code == code
}
