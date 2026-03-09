package service

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

type APIError struct {
	HTTPStatus int    `json:"-"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Field      string `json:"field,omitempty"`
	Timestamp  string `json:"timestamp"`
}

func (e *APIError) Error() string {
	return e.Message
}

func NewValidationError(field, message string) *APIError {
	return &APIError{
		HTTPStatus: http.StatusBadRequest,
		Code:       "VALIDATION_ERROR",
		Message:    message,
		Field:      field,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}
}

func NewNotFoundError(resourceType, id string) *APIError {
	return &APIError{
		HTTPStatus: http.StatusNotFound,
		Code:       fmt.Sprintf("%s_NOT_FOUND", strings.ToUpper(resourceType)),
		Message:    fmt.Sprintf("%s with ID '%s' not found", resourceType, id),
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}
}

func NewConfirmationRequiredError(message string) *APIError {
	return &APIError{
		HTTPStatus: http.StatusBadRequest,
		Code:       "CONFIRMATION_REQUIRED",
		Message:    message,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}
}

func NewInternalError(message string) *APIError {
	return &APIError{
		HTTPStatus: http.StatusInternalServerError,
		Code:       "INTERNAL_ERROR",
		Message:    message,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}
}
