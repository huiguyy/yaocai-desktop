package ysb

import (
	"fmt"
	"log"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// LoginViaBrowser performs browser-based login for YSB (药师帮)
// Has 易盾 (yidun) slider captcha
func (c *YSBClient) LoginViaBrowser(username, password string) (map[string]string, error) {
	log.Printf("[ysb] Starting browser login for %s", username)

	l := launcher.New().
		Headless(true).
		Leakless(false)

	if browserPath, ok := launcher.LookPath(); ok {
		l = l.Bin(browserPath)
		log.Printf("[ysb] Using browser at: %s", browserPath)
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
	log.Printf("[ysb] Navigating to login page...")
	if err := page.Navigate("https://dian.ysbang.cn/#/login"); err != nil {
		return nil, fmt.Errorf("navigate failed: %w", err)
	}
	page.MustWaitStable()
	time.Sleep(2 * time.Second)

	// Fill phone number
	log.Printf("[ysb] Filling phone: %s", username)
	_, err = page.Eval(fmt.Sprintf(`() => {
		const phone = document.querySelector('input[name=userAccount], input[placeholder*="手机"]');
		if (!phone) return 'phone input not found';
		Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set.call(phone, '%s');
		phone.dispatchEvent(new Event('input', { bubbles: true }));
		return 'ok';
	}`, username))
	if err != nil {
		return nil, fmt.Errorf("fill phone failed: %w", err)
	}
	time.Sleep(300 * time.Millisecond)

	// Fill password
	log.Printf("[ysb] Filling password...")
	_, err = page.Eval(fmt.Sprintf(`() => {
		const pwd = document.querySelector('input#password, input[type="password"]');
		if (!pwd) return 'password input not found';
		Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set.call(pwd, '%s');
		pwd.dispatchEvent(new Event('input', { bubbles: true }));
		return 'ok';
	}`, password))
	if err != nil {
		return nil, fmt.Errorf("fill password failed: %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Click login button
	log.Printf("[ysb] Clicking login button...")
	_, err = page.Eval(`() => {
		const btn = [...document.querySelectorAll("button")].find(b => b.textContent.includes("登录") && b.offsetParent !== null);
		if(btn) { btn.click(); return 'clicked'; }
		return 'button not found';
	}`)
	if err != nil {
		return nil, fmt.Errorf("click login failed: %w", err)
	}
	time.Sleep(3 * time.Second)

	// Handle 易盾 slider captcha if present
	hasSlider, _ := page.Eval(`() => !!document.querySelector(".yidun_modal__title, .yidun_slider, .yidun_bg-img")`)
	if hasSlider.Value.Bool() {
		log.Printf("[ysb] Slider captcha detected, handling...")
		if err := handleYSBSlider(page); err != nil {
			log.Printf("[ysb] Slider handling failed: %v (will try to continue)", err)
		}
	} else {
		log.Printf("[ysb] No slider captcha")
	}

	// Wait for redirect
	for i := 0; i < 15; i++ {
		time.Sleep(1 * time.Second)
		url := page.MustInfo().URL
		if !containsYSB(url, "#/login") {
			log.Printf("[ysb] Redirected to: %s", url)
			time.Sleep(3 * time.Second)
			break
		}
	}

	// Extract Token from localStorage
	tokenResult, err := page.Eval(`() => localStorage.getItem('Token')`)
	if err != nil || tokenResult.Value.Str() == "" {
		tokenResult, err = page.Eval(`() => {
			for (let i = 0; i < localStorage.length; i++) {
				const key = localStorage.key(i);
				if (key.toLowerCase().includes('token')) {
					return localStorage.getItem(key);
				}
			}
			return '';
		}`)
	}

	if tokenResult.Value.Str() == "" {
		return nil, fmt.Errorf("token not found in localStorage after login")
	}

	token := tokenResult.Value.Str()

	// Get more localStorage
	shopIdResult, _ := page.Eval(`() => localStorage.getItem('shopId') || ''`)
	userInfoResult, _ := page.Eval(`() => localStorage.getItem('userInfo') || ''`)

	// Get cookies
	cookies, _ := page.Cookies([]string{"https://dian.ysbang.cn"})
	cookieDict := make(map[string]string)
	for _, c := range cookies {
		cookieDict[c.Name] = c.Value
	}
	cookieDict["Token"] = token
	cookieDict["token"] = token
	cookieDict["shopId"] = shopIdResult.Value.Str()
	cookieDict["userInfo"] = userInfoResult.Value.Str()

	log.Printf("[ysb] Browser login success! token=%dchars, cookies=%d", len(token), len(cookieDict))
	return cookieDict, nil
}

// handleYSBSlider attempts to handle 易盾 slider verification
func handleYSBSlider(page *rod.Page) error {
	const maxAttempts = 5

	for attempt := 0; attempt < maxAttempts; attempt++ {
		log.Printf("[ysb] Slider attempt %d/%d", attempt+1, maxAttempts)

		// Wait for slider image to load
		for i := 0; i < 10; i++ {
			time.Sleep(1 * time.Second)
			loaded, _ := page.Eval(`() => {
				const img = document.querySelector(".yidun_bg-img");
				return img && img.src && img.src.length > 30 && img.naturalWidth > 0;
			}`)
			if loaded.Value.Bool() {
				break
			}
		}

		// Get slider element position
		sliderEl, err := page.Element("div.yidun_slider")
		if err != nil {
			log.Printf("[ysb] Slider element not found, might have passed")
			return nil
		}

		shape, err := sliderEl.Shape()
		if err != nil {
			continue
		}
		box := shape.Box()

		startX := box.X + box.Width/2
		startY := box.Y + box.Height/2

		// Get track width
		trackW := 300.0 // default
		trackEl, err := page.Element(".yidun_control, .yidun_bgimg")
		if err == nil {
			trackShape, err2 := trackEl.Shape()
			if err2 == nil {
				trackW = trackShape.Box().Width
			}
		}

		// Calculate drag distance (55-75% of track width, varying by attempt)
		dragDistance := trackW * (0.55 + float64(attempt%3)*0.1)
		targetX := startX + dragDistance

		// Human-like drag
		mouse := page.Mouse
		mouse.MustMoveTo(startX, startY)
		mouse.MustDown(proto.InputMouseButtonLeft)

		steps := 25
		deltaX := (targetX - startX) / float64(steps)
		for i := 1; i <= steps; i++ {
			x := startX + deltaX*float64(i)
			y := startY + float64(i%3-1)*0.5 // tiny jitter
			mouse.MustMoveTo(x, y)
			delay := 20 + float64(i)/float64(steps)*30
			time.Sleep(time.Duration(delay) * time.Millisecond)
		}

		time.Sleep(200 * time.Millisecond)
		mouse.MustUp(proto.InputMouseButtonLeft)

		// Wait for verification result
		time.Sleep(4 * time.Second)

		// Check success
		success, _ := page.Eval(`() => !!document.querySelector("div.yidun.yidun--success")`)
		sliderGone, _ := page.Eval(`() => !document.querySelector(".yidun_slider")`)

		if success.Value.Bool() || sliderGone.Value.Bool() {
			log.Printf("[ysb] Slider verification passed!")
			return nil
		}

		log.Printf("[ysb] Slider attempt %d failed", attempt+1)

		// Click refresh for next attempt
		refreshBtn, err := page.Element(".yidun_refresh, .yidun_lightimg")
		if err == nil {
			refreshBtn.MustClick()
		}
		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("slider captcha failed after %d attempts", maxAttempts)
}

func containsYSB(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
