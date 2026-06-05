package yyc

import (
	"fmt"
	"log"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

// LoginViaBrowser performs browser-based login for YYC (医药城)
func (c *YYCClient) LoginViaBrowser(username, password string) (map[string]string, error) {
	log.Printf("[yyc] Starting browser login for %s", username)

	l := launcher.New().
		Headless(true).
		Leakless(false)

	if browserPath, ok := launcher.LookPath(); ok {
		l = l.Bin(browserPath)
		log.Printf("[yyc] Using browser at: %s", browserPath)
	}

	defer l.Cleanup()

	browserURL, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("browser launch failed: %w", err)
	}

	browser := rod.New().ControlURL(browserURL).MustConnect()
	defer browser.MustClose()

	page := browser.MustPage("about:blank")
	page.MustSetViewport(1280, 800, 1, false)

	// Navigate to login page
	log.Printf("[yyc] Navigating to login page...")
	if err := page.Navigate("https://mall.yaoex.com/login"); err != nil {
		return nil, fmt.Errorf("navigate failed: %w", err)
	}
	page.MustWaitStable()
	time.Sleep(2 * time.Second)

	// Fill username
	log.Printf("[yyc] Filling username: %s", username)
	_, err = page.Eval(fmt.Sprintf(`() => {
		let input = document.querySelector('#username');
		if (!input) input = document.querySelector('[placeholder*="用户"], [placeholder*="账号"]');
		if (!input) return 'input not found';
		Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set.call(input, '%s');
		input.dispatchEvent(new Event('input', { bubbles: true }));
		input.dispatchEvent(new Event('change', { bubbles: true }));
		return 'ok';
	}`, username))
	if err != nil {
		return nil, fmt.Errorf("fill username failed: %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Fill password
	log.Printf("[yyc] Filling password...")
	_, err = page.Eval(fmt.Sprintf(`() => {
		let input = document.querySelector('#password');
		if (!input) input = document.querySelector('input[type="password"], [placeholder*="密码"]');
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
	log.Printf("[yyc] Clicking login button...")
	_, err = page.Eval(`() => {
		const btn = document.querySelector('.login-btn, button[type="submit"]');
		if (!btn) {
			const buttons = [...document.querySelectorAll('button')];
			const loginBtn = buttons.find(b => b.textContent.includes('登录') || b.textContent.includes('Login'));
			if (loginBtn) { loginBtn.click(); return 'clicked'; }
			return 'button not found';
		}
		btn.click();
		return 'clicked';
	}`)
	if err != nil {
		return nil, fmt.Errorf("click login failed: %w", err)
	}

	// Wait for GeeTest or redirect
	time.Sleep(3 * time.Second)

	// Handle GeeTest v4 if present
	hasGeeTest, _ := page.Eval(`() => !!document.querySelector('.geetest_radar_tip, .geetest_btn, [class*="geetest"]')`)
	if hasGeeTest.Value.Bool() {
		log.Printf("[yyc] GeeTest detected, clicking verification button...")
		page.Eval(`() => {
			const btn = document.querySelector('.geetest_radar_tip, .geetest_btn, .geetest_commit');
			if (btn) btn.click();
		}`)
		time.Sleep(3 * time.Second)
	}

	// Wait for redirect
	log.Printf("[yyc] Waiting for login to complete...")
	for i := 0; i < 15; i++ {
		time.Sleep(1 * time.Second)
		currentURL := page.MustInfo().URL
		if currentURL != "" && !containsYYC(currentURL, "/login") {
			log.Printf("[yyc] Redirected to: %s", currentURL)
			break
		}
	}

	// Extract cookies
	cookies, err := page.Cookies([]string{"https://mall.yaoex.com"})
	if err != nil {
		return nil, fmt.Errorf("get cookies failed: %w", err)
	}

	cookieDict := make(map[string]string)
	for _, c := range cookies {
		cookieDict[c.Name] = c.Value
	}

	// Check localStorage for token
	tokenResult, _ := page.Eval(`() => {
		for (let i = 0; i < localStorage.length; i++) {
			const key = localStorage.key(i);
			if (key.toLowerCase().includes('token')) {
				return localStorage.getItem(key);
			}
		}
		return '';
	}`)
	if tokenResult.Value.Str() != "" {
		cookieDict["token"] = tokenResult.Value.Str()
	}

	if cookieDict["ycgltoken"] == "" && cookieDict["token"] == "" {
		var names []string
		for k := range cookieDict {
			names = append(names, k)
		}
		return nil, fmt.Errorf("no auth token found, cookies: %v", names)
	}

	log.Printf("[yyc] Browser login success! cookies=%d, has_ycgltoken=%v", len(cookieDict), cookieDict["ycgltoken"] != "")
	return cookieDict, nil
}

func containsYYC(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
