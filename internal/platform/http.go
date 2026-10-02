// HTTP boundary helpers keep every response in the same documented envelope.
// Handlers decode once, return AppError, and let WriteError shape the response.
package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

type contextKey string

const requestIDKey contextKey = "request_id"

type AppError struct {
	Status      int                 `json:"-"`
	Code        string              `json:"code"`
	Message     string              `json:"message"`
	FieldErrors map[string][]string `json:"field_errors,omitempty"`
	RetryAfter  int                 `json:"retry_after,omitempty"`
	Cause       error               `json:"-"`
}

func (e *AppError) Error() string {
	if e.Cause != nil {
		return e.Code + ": " + e.Cause.Error()
	}
	return e.Code + ": " + e.Message
}

func Problem(status int, code, message string) *AppError {
	return &AppError{Status: status, Code: code, Message: message}
}

func Validation(fields map[string][]string) *AppError {
	return &AppError{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "请检查标记字段", FieldErrors: fields}
}

func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" || len(requestID) > 100 || !safeRequestID(requestID) {
			bytes := make([]byte, 12)
			if _, err := rand.Read(bytes); err != nil {
				requestID = "req_unavailable"
			} else {
				requestID = "req_" + hex.EncodeToString(bytes)
			}
		}
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, requestID)))
	})
}

func safeRequestID(value string) bool {
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("._:-", character) {
			continue
		}
		return false
	}
	return true
}

func RecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				WriteError(w, r, fmt.Errorf("panic: %v", recovered))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func DecodeJSON(w http.ResponseWriter, r *http.Request, target any) *AppError {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return Problem(http.StatusBadRequest, "invalid_json", "请求体不是有效的 JSON")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Problem(http.StatusBadRequest, "invalid_json", "请求体只能包含一个 JSON 对象")
	}
	return nil
}

func WriteData(w http.ResponseWriter, r *http.Request, status int, data any) {
	WriteJSON(w, status, map[string]any{
		"data": data,
		"meta": map[string]string{"request_id": RequestID(r.Context())},
	})
}

func WriteList(w http.ResponseWriter, r *http.Request, data any, nextCursor string) {
	WriteJSON(w, http.StatusOK, map[string]any{
		"data": data,
		"page": map[string]string{"next_cursor": nextCursor},
		"meta": map[string]string{"request_id": RequestID(r.Context())},
	})
}

func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	appError := &AppError{}
	if !errors.As(err, &appError) {
		log.Printf("request_id=%s internal_error=%v", RequestID(r.Context()), err)
		appError = &AppError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "服务暂时不可用", Cause: err}
	}
	if appError.Status == 0 {
		appError.Status = http.StatusInternalServerError
	}
	payload := map[string]any{
		"code":       appError.Code,
		"message":    appError.Message,
		"request_id": RequestID(r.Context()),
	}
	if len(appError.FieldErrors) > 0 {
		payload["field_errors"] = appError.FieldErrors
	}
	if appError.RetryAfter > 0 {
		payload["retry_after"] = appError.RetryAfter
		w.Header().Set("Retry-After", fmt.Sprint(appError.RetryAfter))
	}
	WriteJSON(w, appError.Status, map[string]any{"error": payload})
}

func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
