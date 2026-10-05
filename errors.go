package surriti

import "errors"

var (
	ErrConfig     = errors.New("surriti: configuration error")
	ErrConnection = errors.New("surriti: connection error")
	ErrSchema     = errors.New("surriti: schema error")
	ErrLLM        = errors.New("surriti: llm error")
	ErrNotFound   = errors.New("surriti: not found")
)
