package hub

import "github.com/egoist/mygo/ui"

type onboardingState struct{}
type settingsState struct{}

func (h *Hub) onboardingView(c *ui.Context) {}
func (h *Hub) settingsModal(c *ui.Context)  {}
