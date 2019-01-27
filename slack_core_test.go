package main

import (
	"go.uber.org/zap/zapcore"
	"testing"
)

func TestCore_CheckZapCoreInterface(t *testing.T) {
	var _ zapcore.Core = &SlackCore{}
}
