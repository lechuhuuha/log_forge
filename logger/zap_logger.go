package logger

import "go.uber.org/zap"

type ZapLogger struct {
	l *zap.Logger
}

func NewZapLogger(l *zap.Logger) *ZapLogger {
	return &ZapLogger{
		l: l,
	}
}

func (z *ZapLogger) Info(msg string, fields ...Field)  { z.l.Info(msg, toZap(fields...)...) }
func (z *ZapLogger) Warn(msg string, fields ...Field)  { z.l.Warn(msg, toZap(fields...)...) }
func (z *ZapLogger) Error(msg string, fields ...Field) { z.l.Error(msg, toZap(fields...)...) }
func (z *ZapLogger) Debug(msg string, fields ...Field) { z.l.Debug(msg, toZap(fields...)...) }
func (z *ZapLogger) Fatal(msg string, fields ...Field) { z.l.Fatal(msg, toZap(fields...)...) }

func toZap(fs ...Field) []zap.Field {
	out := make([]zap.Field, 0, len(fs))
	for _, f := range fs {
		out = append(out, zap.Any(f.Key, f.Value))
	}
	return out
}
