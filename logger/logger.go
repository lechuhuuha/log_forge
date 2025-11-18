package logger

type Field struct {
	Key   string
	Value any
}

type Logger interface {
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
	Debug(msg string, fields ...Field)
	Fatal(msg string, fields ...Field)
}

func F(key string, val any) Field { return Field{Key: key, Value: val} }
