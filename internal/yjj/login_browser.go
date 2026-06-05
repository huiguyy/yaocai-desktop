package yjj

import (
	"fmt"
	"log"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// LoginViaBrowser performs browser-based login for YJJ (药九九)
// Has Alibaba NC slider captcha - handled by waiting for auto-pass or retry
func (c *YJJClient) LoginViaBrowser(username, password string) (map[string]string, error) {
	log.Printf("[yjj] Starting browser login for %s", username)

	l := launcher.New().
		Headless(true).
		Leakless(false)

	if browserPath, ok := launcher.LookPath(); ok {
		l = l.Bin(browserPath)
		log.Printf("[yjj] Using browser at: %s", browserPath)
	}

	defer l.Cleanup()

	browserURL, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("browser launch failed: %w", err)
	}

	browser := rod.New().ControlURL(browserURL).MustConnect()
	defer browser.MustClose()

	page := browser.MustPage("about:blank")
	page.MustSetViewport(1920, 1080, 1, false)

	// Navigate to login page
	log.Printf("[yjj] Navigating to login page...")
	if err := page.Navigate("https://www.yyjzt.com/login"); err != nil {
		return nil, fmt.Errorf("navigate failed: %w", err)
	}
	page.MustWaitStable()
	time.Sleep(5 * time.Second)

	// Check if already logged in
	cookies, _ := page.Cookies([]string{"https://www.yyjzt.com"})
	for _, c := range cookies {
		if c.Name == "yjj-token" && c.Value != "" {
			log.Printf("[yjj] Already logged in with existing cookie")
			cookieDict := make(map[string]string)
			for _, c := range cookies {
				cookieDict[c.Name] = c.Value
			}
			return cookieDict, nil
		}
	}

	// Fill phone number
	log.Printf("[yjj] Filling phone: %s", username)
	_, err = page.Eval(fmt.Sprintf(`() => {
		let input = document.querySelector('input[name="phone"], input[placeholder*="手机"], input[placeholder*="账号"]');
		if (!input) input = document.querySelector('input[type="tel"], input[type="text"]');
		if (!input) return 'input not found';
		Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set.call(input, '%s');
		input.dispatchEvent(new Event('input', { bubbles: true }));
		input.dispatchEvent(new Event('change', { bubbles: true }));
		return 'ok';
	}`, username))
	if err != nil {
		return nil, fmt.Errorf("fill phone failed: %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Fill password
	log.Printf("[yjj] Filling password...")
	_, err = page.Eval(fmt.Sprintf(`() => {
		let input = document.querySelector('input[type="password"], input[placeholder*="密码"]');
		if (!input) return 'password input not found';
		Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set.call(input, '%s');
		input.dispatchEvent(new Event('input', { bubbles: true }));
		input.dispatchEvent(new Event('change', { bubbles: true }));
		return 'ok';
	}`, password))
	if err != nil {
		return nil, fmt.Errorf("fill password failed: %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Click login button
	log.Printf("[yjj] Clicking login button...")
	_, err = page.Eval(`() => {
		const btn = document.querySelector('.login-btn, .btn-login, button[type="submit"]');
		if (btn) { btn.click(); return 'clicked'; }
		const buttons = [...document.querySelectorAll('button')];
		const loginBtn = buttons.find(b => b.textContent.includes('登录') || b.textContent.includes('Login'));
		if (loginBtn) { loginBtn.click(); return 'clicked'; }
		return 'button not found';
	}`)
	if err != nil {
		return nil, fmt.Errorf("click login failed: %w", err)
	}

	// Wait for NC captcha or login completion
	log.Printf("[yjj] Waiting for NC captcha or redirect...")
	for i := 0; i < 30; i++ {
		time.Sleep(2 * time.Second)

		// Check if redirected (login success)
		currentURL := page.MustInfo().URL
		if currentURL != "" && !containsYJJ(currentURL, "/login") {
			log.Printf("[yjj] Redirected to: %s", currentURL)
			break
		}

		// Check for yjj-token cookie
		cookies, _ = page.Cookies([]string{"https://www.yyjzt.com"})
		for _, c := range cookies {
			if c.Name == "yjj-token" && c.Value != "" {
				log.Printf("[yjj] Got yjj-token cookie!")
				goto yjjLoginDone
			}
		}

		// Handle NC slider
		hasNC, _ := page.Eval(`() => !!document.querySelector('#nc_1_n1z, .nc_iconfont, [class*="nc_"], .btn_slide')`)
		if hasNC.Value.Bool() {
			log.Printf("[yjj] NC slider detected, attempting to handle...")
			if err := handleNCSlider(page); err != nil {
				log.Printf("[yjj] NC slider attempt failed: %v", err)
			}
		}

		if i%5 == 4 {
			log.Printf("[yjj] Still waiting... (%d/30)", i+1)
		}
	}

yjjLoginDone:
	time.Sleep(3 * time.Second)

	// Extract cookies
	cookies, _ = page.Cookies([]string{"https://www.yyjzt.com"})
	cookieDict := make(map[string]string)
	for _, c := range cookies {
		cookieDict[c.Name] = c.Value
	}

	// Get localStorage
	lsUserBasicId, _ := page.Eval(`() => localStorage.getItem('userBasicId') || ''`)
	lsCompanyId, _ := page.Eval(`() => localStorage.getItem('companyId') || ''`)
	cookieDict["userBasicId"] = lsUserBasicId.Value.Str()
	cookieDict["companyId"] = lsCompanyId.Value.Str()

	if cookieDict["yjj-token"] == "" {
		var names []string
		for k := range cookieDict {
			names = append(names, k)
		}
		return nil, fmt.Errorf("no yjj-token found, cookies: %v", names)
	}

	log.Printf("[yjj] Browser login success! token=%dchars", len(cookieDict["yjj-token"]))
	return cookieDict, nil
}

// handleNCSlider handles Alibaba NC slider captcha
func handleNCSlider(page *rod.Page) error {
	sliderEl, err := page.Element("#nc_1_n1z, .btn_slide, .nc_iconfont")
	if err != nil {
		return fmt.Errorf("slider element not found: %w", err)
	}

	shape, err := sliderEl.Shape()
	if err != nil {
		return fmt.Errorf("slider shape error: %w", err)
	}
	box := shape.Box()

	startX := box.X + box.Width/2
	startY := box.Y + box.Height/2

	// Get slider track width
	trackEl, err := page.Element(".nc_scale, .nc-lang-cfg, [class*='scale']")
	if err != nil {
		return fmt.Errorf("track element not found: %w", err)
	}
	trackShape, err := trackEl.Shape()
	if err != nil {
		return fmt.Errorf("track shape error: %w", err)
	}
	trackBox := trackShape.Box()
	targetX := trackBox.X + trackBox.Width - 20

	// Slide with human-like movement using MoveLinear
	mouse := page.Mouse
	mouse.MustMoveTo(startX, startY)
	mouse.MustDown(proto.InputMouseButtonLeft)

	// Move to target with steps
	steps := 25
	deltaX := (targetX - startX) / float64(steps)
	for i := 1; i <= steps; i++ {
		x := startX + deltaX*float64(i)
		y := startY + float64(i%3-1)*0.5
		mouse.MustMoveTo(x, y)
		time.Sleep(time.Duration(30+float64(i)/float64(steps)*20) * time.Millisecond)
	}

	mouse.MustUp(proto.InputMouseButtonLeft)
	time.Sleep(3 * time.Second)
	return nil
}

func containsYJJ(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
