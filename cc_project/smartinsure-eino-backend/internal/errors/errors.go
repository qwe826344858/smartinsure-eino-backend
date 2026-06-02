package errors

import (
	"encoding/json"
	"net/http"
)

const (
	CodeInvalidArgument = "INVALID_ARGUMENT"
	CodeRateLimited     = "RATE_LIMITED"
	CodeUpstreamTimeout = "UPSTREAM_TIMEOUT"
	CodeUpstreamError   = "UPSTREAM_ERROR"
	CodeInternal        = "INTERNAL_ERROR"
	CodeNotImplemented  = "NOT_IMPLEMENTED"
)

type AppError struct {
	Code       string
	Message    string
	StatusCode int
}

func (e *AppError) Error() string {
	return e.Message
}

func New(code, message string, statusCode int) *AppError {
	return &AppError{Code: code, Message: message, StatusCode: statusCode}
}

func InvalidArgument(message string) *AppError {
	if message == "" {
		message = "请求参数非法"
	}
	return New(CodeInvalidArgument, message, http.StatusBadRequest)
}

func RateLimited(message string) *AppError {
	if message == "" {
		message = "请求过于频繁"
	}
	return New(CodeRateLimited, message, http.StatusTooManyRequests)
}

func UpstreamTimeout(message string) *AppError {
	if message == "" {
		message = "上游服务超时"
	}
	return New(CodeUpstreamTimeout, message, http.StatusGatewayTimeout)
}

func Upstream(message string) *AppError {
	if message == "" {
		message = "上游服务异常"
	}
	return New(CodeUpstreamError, message, http.StatusBadGateway)
}

func Internal(message string) *AppError {
	if message == "" {
		message = "系统内部错误"
	}
	return New(CodeInternal, message, http.StatusInternalServerError)
}

func NotImplemented(message string) *AppError {
	if message == "" {
		message = "功能尚未实现"
	}
	return New(CodeNotImplemented, message, http.StatusNotImplemented)
}

type Response struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func WriteHTTP(w http.ResponseWriter, err error) {
	appErr, ok := err.(*AppError)
	if !ok {
		appErr = Internal("")
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(appErr.StatusCode)
	_ = json.NewEncoder(w).Encode(Response{
		Error: ErrorBody{Code: appErr.Code, Message: appErr.Message},
	})
}
