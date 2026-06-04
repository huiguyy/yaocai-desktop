//go:build ignore
// +build ignore

// This file requires go-rod which needs browser download.
// It's excluded from regular builds. Build with -tags browser to include.

package ysb

import (
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// LoginViaBrowser performs browser-based login with slider captcha handling
// Uses go-rod (devtools protocol) instead of Python's CloakBrowser/pyppeteer
func (c *YSBClient) LoginViaBrowser(username, password, proxy string) error {
	// Launch browser
	l := launcher.New().
		Headless(true).
		Leakless(true)

	if proxy != "" {
		l = l.Proxy(proxy)
	}

	defer l.Cleanup() // remove chromium flags

	url, err := l.Launch()
	if err != nil {
		return fmt.Errorf("browser launch failed: %w", err)
	}

	browser := rod.New().ControlURL(url).MustConnect()
	defer browser.MustClose()

	page := browser.MustPage("about:blank")
	page.MustSetViewport(1920, 1080, 1, false)

	// Navigate to login page
	if err := page.Navigate("https://dian.ysbang.cn/#/login"); err != nil {
		return fmt.Errorf("navigate to login page failed: %w", err)
	}
	page.MustWaitStable()
	time.Sleep(2 * time.Second)

	// Fill phone number (Vue v-model needs native setter)
	_, err = page.Eval(fmt.Sprintf(`() => {
		const phone = document.querySelector('input[name=userAccount]');
		if (phone) {
			Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set.call(phone, '%s');
			phone.dispatchEvent(new Event('input', { bubbles: true }));
		}
	}`, username))
	if err != nil {
		return fmt.Errorf("fill phone failed: %w", err)
	}
	time.Sleep(300 * time.Millisecond)

	// Fill password
	_, err = page.Eval(fmt.Sprintf(`() => {
		const pwd = document.querySelector('input#password');
		if (pwd) {
			Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set.call(pwd, '%s');
			pwd.dispatchEvent(new Event('input', { bubbles: true }));
		}
	}`, password))
	if err != nil {
		return fmt.Errorf("fill password failed: %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Click login button
	_, err = page.Eval(`() => { 
		const btn = [...document.querySelectorAll("button")].find(b => b.textContent.includes("登录") && b.offsetParent !== null); 
		if(btn) btn.click(); 
	}`)
	if err != nil {
		return fmt.Errorf("click login failed: %w", err)
	}
	fmt.Println("YSB login: clicked login button")
	time.Sleep(3 * time.Second)

	// Handle 易盾 slider captcha
	hasSlider, _ := page.Eval(`() => !!document.querySelector(".yidun_modal__title")`)
	if hasSlider.Value.Bool() {
		fmt.Println("YSB login: slider captcha detected, handling...")
		if err := c.handleSliderCaptcha(page); err != nil {
			return fmt.Errorf("slider captcha failed: %w", err)
		}
	} else {
		fmt.Println("YSB login: no slider captcha")
	}

	// Wait for page redirect (away from login page)
	for i := 0; i < 15; i++ {
		time.Sleep(1 * time.Second)
		url := page.MustInfo().URL
		if !contains(url, "#/login") {
			fmt.Printf("YSB login: page redirected to %s\n", url)
			time.Sleep(3 * time.Second)
			break
		}
	}

	// Extract Token from localStorage
	token, err := page.Eval(`() => localStorage.getItem('Token')`)
	if err != nil || !token.Value.IsString() || token.Value.String() == "" {
		// Try other storage locations
		token, err = page.Eval(`() => {
			// Try all localStorage keys with 'token'
			for (let i = 0; i < localStorage.length; i++) {
				const key = localStorage.key(i);
				if (key.toLowerCase().includes('token')) {
					return localStorage.getItem(key);
				}
			}
			// Try sessionStorage
			for (let i = 0; i < sessionStorage.length; i++) {
				const key = sessionStorage.key(i);
				if (key.toLowerCase().includes('token')) {
					return sessionStorage.getItem(key);
				}
			}
			return null;
		}`)
	}

	if err != nil || !token.Value.IsString() || token.Value.String() == "" {
		return fmt.Errorf("failed to extract token from browser storage")
	}

	c.Token = token.Value.String()
	fmt.Printf("YSB login: token obtained (%d chars)\n", len(c.Token))
	return nil
}

// handleSliderCaptcha handles the 易盾 (yidun) slider verification
func (c *YSBClient) handleSliderCaptcha(page *rod.Page) error {
	const maxAttempts = 8

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// Wait for slider image to load
		for i := 0; i < 15; i++ {
			time.Sleep(1 * time.Second)
			loaded, _ := page.Eval(`() => { 
				const img = document.querySelector(".yidun_bg-img"); 
				return img && img.src && img.src.length > 30 && img.naturalWidth > 0; 
			}`)
			if loaded.Value.Bool() {
				break
			}
		}

		// Get slider layout info
		layout, err := page.Eval(`() => { 
			const bg = document.querySelector(".yidun_bg-img"); 
			const ctrl = document.querySelector(".yidun_control"); 
			const sr = (el) => { 
				if(!el) return null; 
				const r = el.getBoundingClientRect(); 
				return {x:r.x, y:r.y, w:r.width, h:r.height}; 
			}; 
			return {bg: {rect: sr(bg), nw: bg ? bg.naturalWidth : 0}, ctrl: sr(ctrl)}; 
		}`)
		if err != nil {
			fmt.Printf("YSB slider: layout query failed: %v\n", err)
			continue
		}

		// TODO: Image-based gap detection
		// For now, use a heuristic approach — we'll improve this with OpenCV-like detection
		// The Python version uses cv2 template matching on alpha channel
		// Go equivalent: we can use gocv or a simpler edge detection approach

		// Get bg and puzzle image URLs
		bgSrc, _ := page.Eval(`() => document.querySelector(".yidun_bg-img")?.src`)
		tpSrc, _ := page.Eval(`() => document.querySelector(".yidun_jigsaw")?.src`)

		fmt.Printf("YSB slider: attempt %d/%d, bg=%s... tp=%s...\n",
			attempt+1, maxAttempts,
			truncate(bgSrc.Value.String(), 60),
			truncate(tpSrc.Value.String(), 60))

		// Download images and detect gap position
		// This is the key challenge — we need image processing in Go
		dragDistance, err := c.detectSliderGap(bgSrc.Value.String(), tpSrc.Value.String(), layout.Value)
		if err != nil {
			fmt.Printf("YSB slider: gap detection failed: %v\n", err)
			continue
		}

		// Get slider element bounding box
		slider, err := page.Element("div.yidun_slider")
		if err != nil {
			fmt.Println("YSB slider: slider element not found, might already passed")
			return nil
		}

		box, err := slider.Box()
		if err != nil {
			fmt.Println("YSB slider: slider not visible")
			continue
		}

		startX := box.X + box.Width/2
		startY := box.Y + box.Height/2

		// Hide overlay elements
		page.Eval(`() => {
			document.querySelectorAll('.yidun_cover-frame').forEach(el => el.style.display = 'none');
			document.querySelectorAll('.yidun_tips, .yidun_tips__content').forEach(el => el.style.pointerEvents = 'none');
		}`)

		// Use CDP mouse events for human-like sliding
		targetX := startX + float64(dragDistance)

		// Generate human-like trajectory
		trajectory := generateHumanTrajectory(startX, startY, targetX, startY)

		// Mouse down
		mouse := page.Mouse
		mouse.Down(proto.InputDispatchMouseEventTypeMousePressed, 1)

		// Move along trajectory with time deltas
		for i, point := range trajectory {
			mouse.Move(point.X, point.Y, 0)
			delta := generateTimeDelta(i, len(trajectory), dragDistance)
			time.Sleep(time.Duration(delta) * time.Millisecond)
		}

		// Mouse up
		mouse.Up(proto.InputDispatchMouseEventTypeMouseReleased, 1)

		time.Sleep(4 * time.Second)

		// Check success
		success, _ := page.Eval(`() => !!document.querySelector("div.yidun.yidun--success")`)
		sliderGone, _ := page.Eval(`() => !document.querySelector(".yidun_slider")`)

		if success.Value.Bool() || sliderGone.Value.Bool() {
			fmt.Println("YSB slider: verification passed!")
			return nil
		}

		fmt.Printf("YSB slider: failed attempt %d/%d\n", attempt+1, maxAttempts)

		// Click refresh button
		refreshBtn, err := page.Element(".yidun_refresh, .yidun_lightimg")
		if err == nil {
			refreshBtn.MustClick()
			time.Sleep(time.Duration(1+rand.Intn(2)) * time.Second)
		}

		// Cool down
		cooldown := time.Duration(min(2+attempt*2, 10)) * time.Second
		fmt.Printf("YSB slider: cooling down %v\n", cooldown)
		time.Sleep(cooldown)
	}

	return fmt.Errorf("slider captcha failed after %d attempts", maxAttempts)
}

// detectSliderGap detects the gap position in the slider captcha image
// This is the critical piece — Python uses cv2 template matching
// Go options: gocv (OpenCV bindings), or pure Go image processing
func (c *YSBClient) detectSliderGap(bgURL, tpURL string, layout interface{}) (int, error) {
	// TODO: Implement proper gap detection
	// Options:
	// 1. Use gocv (requires OpenCV installation) — most accurate
	// 2. Use pure Go image processing (image.Compare, edge detection)
	// 3. Use a remote API for detection
	// 4. Use a pre-trained model

	// For prototype, return a heuristic value
	// The actual implementation needs image download + gap detection
	// This is the #1 challenge for Go rewrite

	return 0, fmt.Errorf("slider gap detection not yet implemented")
}

// generateHumanTrajectory creates a human-like mouse trajectory
// Based on Python's _generate_human_trajectory (Bézier curve + overshoot)
func generateHumanTrajectory(startX, startY, targetX, targetY float64) []proto.Point {
	const numPoints = 30
	points := make([]proto.Point, numPoints)

	dx := targetX - startX
	dy := targetY - startY

	// Bézier control points with slight randomness
	cp1x := startX + dx*0.25 + rand.Float64()*20 - 10
	cp1y := startY + dy*0.1 + rand.Float64()*6 - 3
	cp2x := startX + dx*0.75 + rand.Float64()*20 - 10
	cp2y := startY + dy*0.9 + rand.Float64()*6 - 3

	for i := 0; i < numPoints; i++ {
		t := float64(i) / float64(numPoints-1)
		// Cubic Bézier
		x := cubicBezier(t, startX, cp1x, cp2x, targetX)
		y := cubicBezier(t, startY, cp1y, cp2y, targetY)

		// Add overshoot at ~80% progress
		if t > 0.75 && t < 0.9 && rand.Float64() < 0.23 {
			x += rand.Float64()*21 - 3 // overshoot 3-21px
		}

		// Small random jitter
		x += rand.Float64()*2 - 1
		y += rand.Float64()*2 - 1

		points[i] = proto.Point{X: x, Y: y}
	}

	// Ensure final point is exact
	points[numPoints-1] = proto.Point{X: targetX, Y: targetY}

	return points
}

func cubicBezier(t, p0, p1, p2, p3 float64) float64 {
	u := 1 - t
	return u*u*u*p0 + 3*u*u*t*p1 + 3*u*t*t*p2 + t*t*t*p3
}

// generateTimeDelta generates time deltas between trajectory points
// Matches Python's _generate_time_deltas
func generateTimeDelta(idx, total, distance int) float64 {
	// Human-like: slower at start and end, faster in middle
	progress := float64(idx) / float64(total)

	// Base delta depends on distance
	baseDelta := 15.0 + 10.0*math.Sin(progress*math.Pi)

	// Add micro-pauses
	if rand.Float64() < 0.12 { // ~3.8 pauses per trajectory
		baseDelta += rand.Float64() * 80
	}

	// Acceleration at start, deceleration at end
	if progress < 0.15 {
		baseDelta *= 1.5
	} else if progress > 0.85 {
		baseDelta *= 1.8
	}

	return baseDelta
}

// Helper functions
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
