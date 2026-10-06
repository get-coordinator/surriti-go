package surriti

import (
	"errors"

	"github.com/get-coordinator/surriti-go/internal/providerhttp"
)

var (
	ErrConfig     = providerhttp.ErrConfig
	ErrConnection = errors.New("surriti: connection error")
	ErrSchema     = errors.New("surriti: schema error")
	ErrLLM        = providerhttp.ErrLLM
	ErrNotFound   = errors.New("surriti: not found")
)
