package exceptions

import "errors"

// 在 Go 中，习惯以 "Err" 开头命名错误变量

var (
	// ErrBUCTCourse 基础错误 (通常用于包装其他错误，或者作为通用错误)
	ErrBUCTCourse = errors.New("buct course general error")

	// ErrLogin 对应 Python 的 LoginError
	ErrLogin = errors.New("login failed")

	// ErrNetwork 对应 Python 的 NetworkError
	ErrNetwork = errors.New("network communication error")

	// ErrParse 对应 Python 的 ParseError
	ErrParse = errors.New("failed to parse response data")

	// ErrRateLimit 对应 Python 的 RateLimitError
	ErrRateLimit = errors.New("rate limit exceeded")
)
