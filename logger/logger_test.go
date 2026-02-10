package logger

import (
	"io"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestFieldHelper(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value any
	}{
		{name: "int value", key: "k", value: 123},
		{name: "string value", key: "name", value: "v"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			f := F(tc.key, tc.value)
			if f.Key != tc.key || f.Value != tc.value {
				t.Fatalf("unexpected field: %+v", f)
			}
		})
	}
}

func TestNopLoggerMethods(t *testing.T) {
	l := NewNop()
	cases := []struct {
		name string
		call func()
	}{
		{name: "debug", call: func() { l.Debug("debug", F("k", "v")) }},
		{name: "info", call: func() { l.Info("info", F("k", "v")) }},
		{name: "warn", call: func() { l.Warn("warn", F("k", "v")) }},
		{name: "error", call: func() { l.Error("error", F("k", "v")) }},
		{name: "fatal", call: func() { l.Fatal("fatal", F("k", "v")) }},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tc.call()
		})
	}
}

func TestZapLoggerMethodsAndToZap(t *testing.T) {
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.AddSync(io.Discard),
		zap.DebugLevel,
	)
	base := zap.New(core)
	l := NewZapLogger(base)

	cases := []struct {
		name string
		call func()
	}{
		{name: "debug", call: func() { l.Debug("debug", F("k", "v")) }},
		{name: "info", call: func() { l.Info("info", F("k", "v")) }},
		{name: "warn", call: func() { l.Warn("warn", F("k", "v")) }},
		{name: "error", call: func() { l.Error("error", F("k", "v")) }},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tc.call()
		})
	}

	t.Run("toZap converts fields", func(t *testing.T) {
		fields := toZap(F("a", 1), F("b", "x"))
		if len(fields) != 2 {
			t.Fatalf("expected 2 zap fields, got %d", len(fields))
		}
	})
}
