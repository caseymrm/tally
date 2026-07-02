// WizCamera is a macOS menu bar app that turns on a smart light outside
// your office when your camera is in use, so the household knows you're in
// a meeting. Green dot in the menu bar when you're available, red when
// you're busy; click to override the status or pick which light to control.
package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/caseymrm/menuet"
	"github.com/caseymrm/wizcamera/pkg/camera"
	"github.com/caseymrm/wizcamera/pkg/light"
	_ "github.com/caseymrm/wizcamera/pkg/light/wiz" // register the Wiz provider
)

const (
	defaultsKeyLight = "selectedLight"
	// reassertInterval keeps the bulb red during a meeting even if it
	// lost power or missed a packet. We never re-assert "off" so the
	// bulb can still be used as a normal light outside meetings.
	reassertInterval = time.Minute
	scanTimeout      = 3 * time.Second
)

type overrideMode int

const (
	overrideNone overrideMode = iota
	overrideBusy
	overrideAvailable
)

const statusUnknown light.Status = -1

type statusApp struct {
	mu sync.Mutex

	cameraOn      bool
	override      overrideMode
	overrideFor   time.Duration // 0 means indefinite
	overrideUntil time.Time     // zero means indefinite
	overrideTimer *time.Timer

	lightCfg *light.Config
	light    light.Light
	lightErr error
	lastSent light.Status

	scanning   bool
	discovered []light.Config

	apply chan struct{}
}

func newStatusApp() *statusApp {
	s := &statusApp{
		lastSent: statusUnknown,
		apply:    make(chan struct{}, 1),
	}
	var cfg light.Config
	if err := menuet.Defaults().Unmarshal(defaultsKeyLight, &cfg); err == nil && cfg.Provider != "" {
		if l, err := light.Connect(cfg); err == nil {
			s.lightCfg = &cfg
			s.light = l
		} else {
			log.Printf("connecting saved light: %v", err)
		}
	}
	return s
}

// busyLocked reports the effective status; callers must hold s.mu.
func (s *statusApp) busyLocked() bool {
	switch s.override {
	case overrideBusy:
		return true
	case overrideAvailable:
		return false
	}
	return s.cameraOn
}

// stateChanged refreshes the menu bar dot and pokes the light applier.
func (s *statusApp) stateChanged() {
	s.mu.Lock()
	busy := s.busyLocked()
	s.mu.Unlock()
	title := "🟢"
	if busy {
		title = "🔴"
	}
	menuet.App().SetMenuState(&menuet.MenuState{Title: title})
	menuet.App().MenuChanged()
	select {
	case s.apply <- struct{}{}:
	default:
	}
}

func (s *statusApp) watchCamera(ctx context.Context) {
	for on := range camera.Watch(ctx) {
		s.mu.Lock()
		s.cameraOn = on
		s.mu.Unlock()
		s.stateChanged()
	}
}

// applyLoop pushes status changes to the light. It re-asserts Busy
// periodically (so the light stays red through power blips) but sends Free
// only on transitions, leaving the bulb alone for everyday use.
func (s *statusApp) applyLoop(ctx context.Context) {
	ticker := time.NewTicker(reassertInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.apply:
		case <-ticker.C:
		}

		s.mu.Lock()
		l := s.light
		desired := light.Free
		if s.busyLocked() {
			desired = light.Busy
		}
		lastSent := s.lastSent
		s.mu.Unlock()

		if l == nil || (desired == lastSent && desired == light.Free) {
			continue
		}
		err := l.SetStatus(desired)

		s.mu.Lock()
		s.lightErr = err
		if err == nil && s.light == l {
			s.lastSent = desired
			// Persist any address change discovery found.
			if cfg := l.Config(); s.lightCfg != nil && cfg.Address != s.lightCfg.Address {
				s.lightCfg = &cfg
				menuet.Defaults().Marshal(defaultsKeyLight, cfg)
			}
		}
		s.mu.Unlock()
		if err != nil {
			log.Printf("setting light %v: %v", desired, err)
			menuet.App().MenuChanged()
		}
	}
}

// shutdown turns the light off if we're the ones who turned it on.
func (s *statusApp) shutdown() {
	s.mu.Lock()
	l := s.light
	lastSent := s.lastSent
	s.mu.Unlock()
	if l != nil && lastSent == light.Busy {
		if err := l.SetStatus(light.Free); err != nil {
			log.Printf("turning light off at exit: %v", err)
		}
	}
}

func (s *statusApp) setOverride(mode overrideMode, d time.Duration) {
	s.mu.Lock()
	if s.overrideTimer != nil {
		s.overrideTimer.Stop()
		s.overrideTimer = nil
	}
	s.override = mode
	s.overrideFor = d
	s.overrideUntil = time.Time{}
	if mode != overrideNone && d > 0 {
		s.overrideUntil = time.Now().Add(d)
		s.overrideTimer = time.AfterFunc(d, s.expireOverride)
	}
	s.mu.Unlock()
	s.stateChanged()
}

func (s *statusApp) expireOverride() {
	s.mu.Lock()
	expired := s.override != overrideNone && !s.overrideUntil.IsZero() && !time.Now().Before(s.overrideUntil)
	if expired {
		s.override = overrideNone
		s.overrideUntil = time.Time{}
		s.overrideTimer = nil
	}
	s.mu.Unlock()
	if expired {
		s.stateChanged()
	}
}

func (s *statusApp) selectLight(cfg light.Config) {
	l, err := light.Connect(cfg)
	if err != nil {
		log.Printf("connecting %s: %v", cfg.Key(), err)
		return
	}
	s.mu.Lock()
	// If we left a previous light red, turn it off before switching.
	prev, prevSent := s.light, s.lastSent
	s.lightCfg = &cfg
	s.light = l
	s.lightErr = nil
	s.lastSent = statusUnknown
	s.mu.Unlock()
	if prev != nil && prevSent == light.Busy {
		go prev.SetStatus(light.Free)
	}
	menuet.Defaults().Marshal(defaultsKeyLight, cfg)
	s.stateChanged()
}

func (s *statusApp) rescan() {
	s.mu.Lock()
	if s.scanning {
		s.mu.Unlock()
		return
	}
	s.scanning = true
	s.mu.Unlock()
	menuet.App().MenuChanged()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
		defer cancel()
		found := light.DiscoverAll(ctx)
		s.mu.Lock()
		s.scanning = false
		s.discovered = found
		s.mu.Unlock()
		menuet.App().MenuChanged()
	}()
}

func fmtDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	h := d / time.Hour
	m := (d % time.Hour) / time.Minute
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	case m > 0:
		return fmt.Sprintf("%dm", m)
	}
	return "less than a minute"
}

func (s *statusApp) menuItems() []menuet.MenuItem {
	s.mu.Lock()
	defer s.mu.Unlock()

	status := "🟢 Available"
	if s.busyLocked() {
		status = "🔴 In a meeting"
	}
	cameraLine := "Camera is off"
	if s.cameraOn {
		cameraLine = "Camera is on"
	}
	items := []menuet.MenuItem{
		{Text: status, FontWeight: menuet.WeightBold},
		{Text: cameraLine, FontSize: 12},
	}

	if s.override != overrideNone {
		mode := "busy"
		if s.override == overrideAvailable {
			mode = "available"
		}
		text := fmt.Sprintf("Showing %s until turned off", mode)
		if !s.overrideUntil.IsZero() {
			text = fmt.Sprintf("Showing %s for %s more", mode, fmtDuration(time.Until(s.overrideUntil)))
		}
		items = append(items,
			menuet.MenuItem{Text: text, FontSize: 12},
			menuet.MenuItem{Text: "Clear Override", Clicked: func() { s.setOverride(overrideNone, 0) }},
		)
	}
	if s.lightErr != nil {
		items = append(items, menuet.MenuItem{Text: "⚠️ Light is unreachable", FontSize: 12})
	}

	items = append(items,
		menuet.MenuItem{Type: menuet.Separator},
		menuet.MenuItem{
			Text:     "Force Busy",
			State:    s.override == overrideBusy,
			Children: s.overrideChildren(overrideBusy),
		},
		menuet.MenuItem{
			Text:     "Force Available",
			State:    s.override == overrideAvailable,
			Children: s.overrideChildren(overrideAvailable),
		},
		menuet.MenuItem{Type: menuet.Separator},
	)

	lightText := "Choose Light"
	if s.lightCfg != nil {
		lightText = "Light: " + s.lightCfg.Name
	}
	items = append(items, menuet.MenuItem{Text: lightText, Children: s.lightChildren})
	return items
}

func (s *statusApp) overrideChildren(mode overrideMode) func() []menuet.MenuItem {
	options := []struct {
		text string
		d    time.Duration
	}{
		{"For 5 Minutes", 5 * time.Minute},
		{"For 1 Hour", time.Hour},
		{"Until Turned Off", 0},
	}
	return func() []menuet.MenuItem {
		s.mu.Lock()
		defer s.mu.Unlock()
		items := make([]menuet.MenuItem, 0, len(options))
		for _, opt := range options {
			opt := opt
			items = append(items, menuet.MenuItem{
				Text:    opt.text,
				State:   s.override == mode && s.overrideFor == opt.d,
				Clicked: func() { s.setOverride(mode, opt.d) },
			})
		}
		return items
	}
}

func (s *statusApp) lightChildren() []menuet.MenuItem {
	s.mu.Lock()
	defer s.mu.Unlock()

	var items []menuet.MenuItem
	selectedShown := false
	for _, cfg := range s.discovered {
		cfg := cfg
		selected := s.lightCfg != nil && s.lightCfg.Key() == cfg.Key()
		selectedShown = selectedShown || selected
		items = append(items, menuet.MenuItem{
			Text:    fmt.Sprintf("%s (%s)", cfg.Name, cfg.Address),
			State:   selected,
			Clicked: func() { s.selectLight(cfg) },
		})
	}
	if s.lightCfg != nil && !selectedShown {
		cfg := *s.lightCfg
		items = append([]menuet.MenuItem{{
			Text:  fmt.Sprintf("%s (%s)", cfg.Name, cfg.Address),
			State: true,
		}}, items...)
	}
	if len(items) == 0 && !s.scanning {
		items = append(items, menuet.MenuItem{Text: "No lights found"})
	}
	items = append(items, menuet.MenuItem{Type: menuet.Separator})
	if s.scanning {
		items = append(items, menuet.MenuItem{Text: "Scanning…"})
	} else {
		items = append(items, menuet.MenuItem{Text: "Rescan Network", Clicked: s.rescan})
	}
	return items
}

func main() {
	app := menuet.App()
	app.Name = "WizCamera"
	app.Label = "com.github.caseymrm.wizcamera"

	s := newStatusApp()
	app.Children = s.menuItems

	wg, ctx := app.GracefulShutdownHandles()
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		s.shutdown()
	}()

	go s.watchCamera(ctx)
	go s.applyLoop(ctx)
	s.rescan()
	s.stateChanged()

	app.RunApplication()
}
