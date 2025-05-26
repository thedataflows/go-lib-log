package log

import (
	"fmt"
)

func Trace(packageName string, msg any) {
	Logger.Trace().Str(KEY_PKG, packageName).Msg(fmt.Sprint(msg))
}

func Tracef(packageName string, format string, msg ...any) {
	Logger.Trace().Str(KEY_PKG, packageName).Msgf(format, msg...)
}

func Debug(packageName string, msg any) {
	Logger.Debug().Str(KEY_PKG, packageName).Msg(fmt.Sprint(msg))
}

func Debugf(packageName string, format string, msg ...any) {
	Logger.Debug().Str(KEY_PKG, packageName).Msgf(format, msg...)
}

func Info(packageName string, msg any) {
	Logger.Info().Str(KEY_PKG, packageName).Msg(fmt.Sprint(msg))
}

func Infof(packageName string, format string, msg ...any) {
	Logger.Info().Str(KEY_PKG, packageName).Msgf(format, msg...)
}

func Warn(packageName string, msg any) {
	Logger.Warn().Str(KEY_PKG, packageName).Msg(fmt.Sprint(msg))
}

func Warnf(packageName string, format string, msg ...any) {
	Logger.Warn().Str(KEY_PKG, packageName).Msgf(format, msg...)
}

func Error(packageName string, err error, msg any) {
	Logger.Error().Str(KEY_PKG, packageName).Err(err).Msg(fmt.Sprint(msg))
}

func Errorf(packageName string, err error, format string, msg ...any) {
	Logger.Error().Str(KEY_PKG, packageName).Err(err).Msgf(format, msg...)
}

func Fatal(packageName string, err error, msg any) {
	Logger.Fatal().Str(KEY_PKG, packageName).Err(err).Msg(fmt.Sprint(msg))
}

func Fatalf(packageName string, err error, format string, msg ...any) {
	Logger.Fatal().Str(KEY_PKG, packageName).Err(err).Msgf(format, msg...)
}

func Panic(packageName string, err error, msg any) {
	Logger.Panic().Str(KEY_PKG, packageName).Err(err).Msg(fmt.Sprint(msg))
}

func Panicf(packageName string, err error, format string, msg ...any) {
	Logger.Panic().Str(KEY_PKG, packageName).Err(err).Msgf(format, msg...)
}

func Print(packageName string, msg any) {
	Logger.WithLevel(NoLevel).Str("level", "-").Str(KEY_PKG, packageName).Msg(fmt.Sprint(msg))
}

func Printf(packageName string, format string, msg ...any) {
	Logger.WithLevel(NoLevel).Str("level", "-").Str(KEY_PKG, packageName).Msgf(format, msg...)
}
