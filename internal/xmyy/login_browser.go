package xmyy

import (
	"fmt"
	"log"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

// LoginViaBrowser performs browser-based login for XMYY
// No captcha - just fill form, click login, extract localStorage token
func (c *XMYYClient) LoginViaBrowser(username, password string) (map[string]string, error) {
	log.Printf("[xmyy] Starting browser login for %s", username)

	// Launch browser
	l := launcher.New().
		Headless(true).
		Leakless(false) // avoid Windows Defender false positive

	if browserPath, ok := launcher.LookPath(); ok {
		l = l.Bin(browserPath)
		log.Printf("[xmyy] Using browser at: %s", browserPath)
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
	log.Printf("[xmyy] Navigating to login page...")
	if err := page.Navigate("https://www.xmyc.com.cn/#/login"); err != nil {
		return nil, fmt.Errorf("navigate failed: %w", err)
	}
	page.MustWaitStable()
	time.Sleep(3 * time.Second)

	// Fill username using native setter (Vue v-model compatible)
	log.Printf("[xmyy] Filling username: %s", username)
	fillUsername := fmt.Sprintf(jsSetInputValue, "input[placeholder=\"请输入用户名/手机号\"]", username)
	_, err = page.Eval(fillUsername)
	if err != nil {
		return nil, fmt.Errorf("fill username failed: %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Fill password
	log.Printf("[xmyy] Filling password...")
	fillPassword := fmt.Sprintf(jsSetInputValue, "input[type=\"password\"], input[placeholder*=\"密码\"]", password)
	_, err = page.Eval(fillPassword)
	if err != nil {
		return nil, fmt.Errorf("fill password failed: %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Check and click agreement checkbox if present
	page.Eval(jsClickCheckbox)
	time.Sleep(300 * time.Millisecond)

	// Click login button
	log.Printf("[xmyy] Clicking login button...")
	_, err = page.Eval(jsClickLoginButton)
	if err != nil {
		return nil, fmt.Errorf("click login failed: %w", err)
	}

	// Wait for login to complete
	log.Printf("[xmyy] Waiting for login response (5s)...")
	time.Sleep(5 * time.Second)

	// Check for error messages on page
	errMsg, _ := page.Eval(jsGetErrorMessage)
	if errMsg.Value.Str() != "" {
		return nil, fmt.Errorf("login error: %s", errMsg.Value.Str)
	}

	// Extract token from localStorage
	tokenResult, _ := page.Eval(jsGetToken)
	token := tokenResult.Value.Str()

	if token == "" {
		// Try alternative: search all localStorage for token key
		tokenResult2, _ := page.Eval(jsSearchTokenInStorage)
		token = tokenResult2.Value.Str()
	}

	if token == "" {
		// Debug: dump all localStorage keys
		debugLS, _ := page.Eval(jsDumpLocalStorage)
		return nil, fmt.Errorf("token not found in localStorage, keys: %s", debugLS.Value.Str())
	}

	// Extract shopId and userInfo
	shopIdResult, _ := page.Eval(`() => localStorage.getItem('HT_CACHE_shopId') || ''`)
	userInfoResult, _ := page.Eval(`() => localStorage.getItem('HT_CACHE_userInfo') || ''`)

	log.Printf("[xmyy] Browser login success! token=%dchars, shopId=%s", len(token), shopIdResult.Value.Str())

	return map[string]string{
		"HT_CACHE_TOKEN":    token,
		"HT_CACHE_shopId":   shopIdResult.Value.Str(),
		"HT_CACHE_userInfo": userInfoResult.Value.Str(),
		"token":             token,
	}, nil
}

// JS helper snippets
const jsSetInputValue = `() => {
	const input = document.querySelector('%s');
	if (!input) return 'input not found';
	const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
	setter.call(input, '%s');
	input.dispatchEvent(new Event('input', { bubbles: true }));
	input.dispatchEvent(new Event('change', { bubbles: true }));
	return 'ok';
}`

const jsClickCheckbox = `() => {
	const checkbox = document.querySelector('input[type="checkbox"]');
	if (checkbox && !checkbox.checked) checkbox.click();
}`

const jsClickLoginButton = `() => {
	const buttons = [...document.querySelectorAll('button')];
	const btn = buttons.find(b => b.textContent.includes('登录') || b.textContent.includes('Login'));
	if (btn) { btn.click(); return 'clicked'; }
	return 'button not found';
}`

const jsGetErrorMessage = `() => {
	const errEl = document.querySelector('.el-message__content, .el-form-item__error, [class*="error"], [class*="Error"]');
	return errEl ? errEl.textContent : '';
}`

const jsGetToken = `() => localStorage.getItem('HT_CACHE_TOKEN') || ''`

const jsSearchTokenInStorage = `() => {
	for (let i = 0; i < localStorage.length; i++) {
		const key = localStorage.key(i);
		if (key.toLowerCase().includes('token')) {
			return localStorage.getItem(key);
		}
	}
	return '';
}`

const jsDumpLocalStorage = `() => {
	const items = {};
	for (let i = 0; i < localStorage.length; i++) {
		const key = localStorage.key(i);
		items[key] = localStorage.getItem(key).substring(0, 50);
	}
	return JSON.stringify(items);
}`
