// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build desktop

package desktop

import (
	"log/slog"

	"github.com/wailsapp/wails/v2/pkg/logger"
)

type wailsLogger struct {
	log *slog.Logger
}

var _ logger.Logger = (*wailsLogger)(nil)

func newWailsLogger(log *slog.Logger) *wailsLogger {
	return &wailsLogger{log: log.With("logger_name", "wails")}
}

const wailsMsg = "wails framework"

func (w *wailsLogger) Print(message string)   { w.log.Debug(wailsMsg, "wailsMessage", message) }
func (w *wailsLogger) Trace(message string)   { w.log.Debug(wailsMsg, "wailsMessage", message) }
func (w *wailsLogger) Debug(message string)   { w.log.Debug(wailsMsg, "wailsMessage", message) }
func (w *wailsLogger) Info(message string)    { w.log.Info(wailsMsg, "wailsMessage", message) }
func (w *wailsLogger) Warning(message string) { w.log.Warn(wailsMsg, "wailsMessage", message) }
func (w *wailsLogger) Error(message string)   { w.log.Error(wailsMsg, "wailsMessage", message) }

func (w *wailsLogger) Fatal(message string) {
	w.log.Error(wailsMsg+" fatal", "wailsMessage", message)
}
