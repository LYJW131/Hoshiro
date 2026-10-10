package core

import (
	"time"

	"github.com/IcyBlue17/hoshiro/internal/model"
)

// PreviewBar puts the bar in a given state without a session, for the
// `-demo` mode and the UI tests (the Tauri version's bar Demo).
func (a *App) PreviewBar(st model.BarState, level float32, alerts []model.BarAlert, answer *model.AskAnswer) {
	a.barMu.Lock()
	a.bar, a.level = st, level
	a.alerts = a.alerts[:0]
	for _, al := range alerts {
		a.alerts = append(a.alerts, Alert{al, time.Now()})
	}
	a.answer = answer
	a.barMu.Unlock()
	a.barChanged()
}
