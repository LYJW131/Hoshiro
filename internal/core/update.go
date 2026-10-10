package core

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/IcyBlue17/hoshiro/internal/model"
)

// In-app updates: checks run 30 s after launch and then daily, from the tray
// and from the About page; the result is cached so every surface agrees.

const (
	checkFailed   = "无法检查更新，请稍后再试"
	installFailed = "更新失败，请稍后再试"
)

// UpdateState is the cached result, the last error, and install progress.
func (a *App) UpdateState() (info *model.UpdateInfo, err string, installing bool, done, total int64) {
	a.updMu.Lock()
	defer a.updMu.Unlock()
	if a.update != nil {
		v := *a.update
		info = &v
	}
	return info, a.updErr, a.installing.Load(), a.progress[0], a.progress[1]
}

// KnownUpdateVersion is the version of an available update, for the tray label.
func (a *App) KnownUpdateVersion() string {
	a.updMu.Lock()
	defer a.updMu.Unlock()
	if a.update != nil && a.update.Available {
		return a.update.Version
	}
	return ""
}

// CheckUpdate asks the update feed; announces a version only the first time it is seen.
func (a *App) CheckUpdate(ctx context.Context) (*model.UpdateInfo, error) {
	if a.Hooks.CheckForUpdate == nil {
		return nil, errors.New(checkFailed)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	info, err := a.Hooks.CheckForUpdate(ctx)
	a.updMu.Lock()
	if err != nil {
		log.Printf("update check failed: %v", err)
		a.updErr = checkFailed
		a.updMu.Unlock()
		a.hub("update")
		return nil, errors.New(checkFailed)
	}
	announce := info.Available && (a.update == nil || a.update.Version != info.Version)
	a.update, a.updErr = info, ""
	a.updMu.Unlock()
	if a.Hooks.RefreshTray != nil {
		a.Hooks.RefreshTray()
	}
	a.hub("update")
	if announce {
		a.hub("update-available")
	}
	return info, nil
}

// InstallUpdate downloads and installs the known update; the caller relaunches on success.
func (a *App) InstallUpdate(ctx context.Context) error {
	if a.installing.Swap(true) {
		return errors.New("正在更新")
	}
	if a.KnownUpdateVersion() == "" {
		if _, err := a.CheckUpdate(ctx); err != nil || a.KnownUpdateVersion() == "" {
			a.installing.Store(false)
			return errors.New("没有可用的更新")
		}
	}
	a.hub("update")
	err := errors.New(installFailed)
	if a.Hooks.InstallUpdate != nil {
		err = a.Hooks.InstallUpdate(ctx, func(done, total int64) {
			a.updMu.Lock()
			a.progress = [2]int64{done, total}
			a.updMu.Unlock()
			a.hub("update")
		})
	}
	if err != nil {
		log.Printf("update install failed: %v", err)
		a.installing.Store(false)
		a.hub("update")
		return errors.New(installFailed)
	}
	return nil
}

// UpdateTrayAction: with a known update install it (progress shows in About), otherwise check now.
func (a *App) UpdateTrayAction(relaunch func()) {
	a.Navigate("settings:about")
	go func() {
		if a.KnownUpdateVersion() != "" {
			if a.InstallUpdate(a.ctx) == nil && relaunch != nil {
				relaunch()
			}
		} else {
			_, _ = a.CheckUpdate(a.ctx)
		}
	}()
}

func (a *App) updateLoop() {
	select {
	case <-a.ctx.Done():
		return
	case <-time.After(30 * time.Second):
	}
	for {
		_, _ = a.CheckUpdate(a.ctx) // failures are only logged
		select {
		case <-a.ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
}
