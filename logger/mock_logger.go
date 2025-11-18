package logger

type nopLogger struct{}

// Debug implements logger.Logger.
func (n nopLogger) Debug(msg string, fields ...Field) {

}

// Error implements logger.Logger.
func (n nopLogger) Error(msg string, fields ...Field) {

}

// Info implements logger.Logger.
func (n nopLogger) Info(msg string, fields ...Field) {

}

// Warn implements logger.Logger.
func (n nopLogger) Warn(msg string, fields ...Field) {

}

// Fatal implements logger.Logger.
func (n nopLogger) Fatal(msg string, fields ...Field) {

}

func NewNop() nopLogger {
	return nopLogger{}
}
