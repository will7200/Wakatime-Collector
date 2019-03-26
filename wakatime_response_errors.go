package main

import (
	"strings"

	"github.com/joomcode/errorx"
)

var (
	responseCodeTrait = errorx.RegisterTrait("ResponseCode")

	wakatimeNamespace = errorx.NewNamespace("wakatime", responseCodeTrait)

	ResponseError = wakatimeNamespace.NewType("ResponseError")
	RateLimited   = wakatimeNamespace.NewType("RateLimited")
	NotFound      = wakatimeNamespace.NewType("Not Found")

	clientNamespace = errorx.NewNamespace("Client")
	Timeout         = clientNamespace.NewType("timeout", errorx.Timeout())
)

func parseError(_err error) error {
	s := _err.Error()
	switch {
	case strings.Contains(s, "[429]") || strings.Contains(s, "(status 429)"):
		return RateLimited.NewWithNoMessage()
	case strings.Contains(s, "[404]") || strings.Contains(s, "(status 404)"):
		return NotFound.NewWithNoMessage()
	case strings.Contains(s, "Client.Timeout"):
		return Timeout.NewWithNoMessage()
	}
	return _err
}
