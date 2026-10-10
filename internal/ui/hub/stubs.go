package hub

import "github.com/egoist/mygo/ui"

type onboardingState struct{}
type histState struct{ limit int }
type dictState struct{}
type usageState struct{}
type settingsState struct{}

func (h *Hub) onboardingView(c *ui.Context) {}
func (h *Hub) historyPage(c *ui.Context)    {}
func (h *Hub) dictionaryPage(c *ui.Context) {}
func (h *Hub) statisticsPage(c *ui.Context) {}
func (h *Hub) settingsModal(c *ui.Context)  {}
