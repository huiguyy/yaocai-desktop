package jyjt

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"yaocai-spider-go/internal/config"
	"yaocai-spider-go/internal/platform"
)

const JYJTDomain = "https://b2b.jzys.com.cn"

// extractJYJTToken extracts the JWT token from cookie_dict.
// The cookie value is a JSON string like `{"value":"<base64>"}` where the base64 decodes to a JWT.
func extractJYJTToken(cookieDict map[string]interface{}) string {
	if cookieDict == nil {
		return ""
	}

	raw, ok := cookieDict["jzyy__Shopping-Access-Token"]
	if !ok {
		return ""
	}

	// Resolve to a string value
	var rawStr string
	switch v := raw.(type) {
	case string:
		rawStr = v
	case map[string]interface{}:
		if inner, ok := v["value"].(string); ok {
			rawStr = inner
		}
	}

	if rawStr == "" {
		return ""
	}

	// Try parsing as JSON {"value":"<base64>"} using interface{} (not string, as values may be non-string)
	var wrapper map[string]interface{}
	if json.Unmarshal([]byte(rawStr), &wrapper) == nil {
		if inner, ok := wrapper["value"].(string); ok {
			rawStr = inner
		}
	}

	// Now rawStr should be a base64-encoded JWT
	var decoded []byte
	var err error

	// Try multiple base64 encodings
	for _, enc := range []struct {
		name string
		dec  func(string) ([]byte, error)
	}{
		{"StdEncoding", base64.StdEncoding.DecodeString},
		{"URLEncoding", base64.URLEncoding.DecodeString},
		{"RawURLEncoding", base64.RawURLEncoding.DecodeString},
		{"RawStdEncoding", base64.RawStdEncoding.DecodeString},
	} {
		decoded, err = enc.dec(rawStr)
		if err == nil {
			break
		}
		padded := rawStr + strings.Repeat("=", (4-len(rawStr)%4)%4)
		decoded, err = enc.dec(padded)
		if err == nil {
			break
		}
	}

	if err != nil {
		// Last resort: maybe it's already the JWT itself
		if strings.HasPrefix(rawStr, "eyJ") {
			return rawStr
		}
		return ""
	}

	decodedStr := string(decoded)

	// Check if decoded is JSON {"value":"<jwt>"}
	var tokenObj map[string]interface{}
	if json.Unmarshal(decoded, &tokenObj) == nil {
		if val, ok := tokenObj["value"].(string); ok {
			return val
		}
	}

	// If decoded is not JSON, maybe it's the JWT directly
	if strings.HasPrefix(decodedStr, "eyJ") {
		return decodedStr
	}

	return ""
}

// JYJTClient is the Go client for 江药集团 platform
type JYJTClient struct {
	CookieDict map[string]interface{}
	token      string
	client     *http.Client
	headers    map[string]string
}

// NewJYJTClient creates a new JYJT client
func NewJYJTClient(cookieDict map[string]interface{}) *JYJTClient {
	jar, _ := cookiejar.New(nil)
	token := extractJYJTToken(cookieDict)
	return &JYJTClient{
		CookieDict: cookieDict,
		token:      token,
		client: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
		},
		headers: map[string]string{
			"Accept":                "application/json, text/plain, */*",
			"Accept-Language":       "zh-CN,zh;q=0.9,en;q=0.8",
			"ci":                    "jyly",
			"Referer":               "https://b2b.jzys.com.cn/productList",
			"Shopping-Access-Token": token,
			"User-Agent":            "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
		},
	}
}

func (c *JYJTClient) Name() string { return "江药集团" }
func (c *JYJTClient) Code() string { return "jyjt" }

func (c *JYJTClient) Login(username, password string) (map[string]string, error) {
	// JYJT纯API登录：POST /web/customer/login，密码加密 MD5(pwd)[8:24]
	// API直接返回JWT token在customerList[0].token中，无需浏览器
	hash := md5.Sum([]byte(password))
	hexHash := fmt.Sprintf("%x", hash)
	encryptedPwd := hexHash[8:24] // 取中间16位

	loginURL := JYJTDomain + "/web/customer/login"
	data := map[string]interface{}{
		"username": username,
		"password": encryptedPwd,
	}
	log.Printf("[jyjt] Login POST to %s with user=%s", loginURL, username)
	body, err := c.postJSON(loginURL, data)
	if err != nil {
		return nil, fmt.Errorf("JYJT login request failed: %w", err)
	}
	log.Printf("[jyjt] Login raw response: %s", string(body[:min(500, len(body))]))

	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("JYJT login parse error: %w", err)
	}

	success, _ := resp["success"].(bool)
	if !success {
		msg, _ := resp["message"].(string)
		return nil, fmt.Errorf("JYJT login failed: %s", msg)
	}

	result, _ := resp["result"].(map[string]interface{})
	if result == nil {
		return nil, fmt.Errorf("JYJT login: no result in response")
	}

	customerList, _ := result["customerList"].([]interface{})
	if len(customerList) == 0 {
		return nil, fmt.Errorf("JYJT login: empty customerList")
	}

	customer, _ := customerList[0].(map[string]interface{})
	token, _ := customer["token"].(string)
	if token == "" {
		return nil, fmt.Errorf("JYJT login: empty token in customerList[0]")
	}

	customerID, _ := customer["id"].(string)
	companyID, _ := customer["companyId"].(string)

	log.Printf("[jyjt] Login success: user=%s companyId=%s token=%s...", username, companyID, token[:min(30, len(token))])

	return map[string]string{
		"jzyy__Shopping-Access-Token": token,
		"jzyy__username":              fmt.Sprintf(`{"value":"%s","expire":null}`, username),
		"companyId":                   companyID,
		"customerId":                  customerID,
	}, nil
}

func (c *JYJTClient) postJSON(urlStr string, data interface{}) ([]byte, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal error: %w", err)
	}
	log.Printf("[jyjt] POST %s body=%s", urlStr, string(jsonData[:min(200, len(jsonData))]))
	req, err := http.NewRequest("POST", urlStr, strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, fmt.Errorf("new request error: %w", err)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http do error: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body error: %w", err)
	}
	log.Printf("[jyjt] POST %s status=%d body=%s", urlStr, resp.StatusCode, string(body[:min(300, len(body))]))
	return body, nil
}

func (c *JYJTClient) SetToken(token string) {
	c.token = token
	c.headers["Shopping-Access-Token"] = token
}

// SearchDrugs implements the Platform interface
func (c *JYJTClient) SearchDrugs(sku platform.DrugSKU, hasTraceCode bool) (*platform.CrawlResult, error) {
	if c.token == "" {
		return nil, fmt.Errorf("JYJT: not authenticated, token is empty")
	}

	searchURL := JYJTDomain + "/web/product/getlistE"

	// Try with empty manufacturer first, then with factory names
	companyList := append([]string{""}, sku.FactoryAlias...)
	companyList = append(companyList, sku.Factory)

	var items []platform.DrugResult
	for _, company := range companyList {
		params := url.Values{
			"searchVal":                   {sku.Name},
			"pageNo":                      {"1"},
			"pageSize":                    {"50"},
			"manufacturer":                {company},
			"productScreenBO.isInventory": {"0"},
		}

		body, err := c.get(searchURL, params)
		if err != nil {
			log.Printf("⚠️ [jyjt] Search HTTP error for %s manufacturer=%s: %v", sku.Name, company, err)
			continue
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(body, &resp); err != nil {
			log.Printf("⚠️ [jyjt] Search JSON parse error: %v", err)
			continue
		}

		codeVal, _ := resp["code"].(float64)
		if int(codeVal) != 200 {
			log.Printf("⚠️ [jyjt] Search API returned code=%v msg=%v", resp["code"], resp["message"])
			continue
		}

		result, _ := resp["result"].(map[string]interface{})
		if result == nil {
			log.Printf("⚠️ [jyjt] No 'result' in response, keys: %v", func() []string { keys := make([]string, 0); for k := range resp { keys = append(keys, k) }; return keys }())
			continue
		}
		pageData, _ := result["pageData"].(map[string]interface{})
		if pageData == nil {
			log.Printf("⚠️ [jyjt] No 'pageData' in result, keys: %v", func() []string { keys := make([]string, 0); for k := range result { keys = append(keys, k) }; return keys }())
			continue
		}

		// pageData uses "records" (actual API), fallback to "list"
		var records []interface{}
		if recs, ok := pageData["records"].([]interface{}); ok {
			records = recs
		} else if recs, ok := pageData["list"].([]interface{}); ok {
			records = recs
		}

		log.Printf("📋 [jyjt] pageData keys: %v, records count: %d", func() []string { keys := make([]string, 0); for k := range pageData { keys = append(keys, k) }; return keys }(), len(records))

		if len(records) == 0 {
			continue
		}

		// Parse items — JYJT API uses non-standard field names:
		//   name (not productName), format (not specification)
		//   price is at top-level (null) or in inventoryList[0].price / orderPrice / showPrice
		for _, item := range records {
			d, ok := item.(map[string]interface{})
			if !ok {
				continue
			}

			factory := toString(d["manufacturer"])
			if company != "" && !strings.Contains(strings.ToLower(factory), strings.ToLower(company)) &&
				!strings.Contains(strings.ToLower(company), strings.ToLower(factory)) {
				continue
			}

			// Get price: prefer orderPrice > showPrice > inventoryList[0].price > price
			var price float64
			if p := toFloat(d["orderPrice"]); p > 0 {
				price = p
			} else if p := toFloat(d["showPrice"]); p > 0 {
				price = p
			} else if invList, ok := d["inventoryList"].([]interface{}); ok && len(invList) > 0 {
				if inv, ok := invList[0].(map[string]interface{}); ok {
					price = toFloat(inv["price"])
				}
			} else {
				price = toFloat(d["price"])
			}

			if price <= 0 || price >= config.GetConfig().PriceAnomalyThreshold {
				log.Printf("📋 [jyjt] Skipped: price=%v drug=%s factory=%s", price, toString(d["name"]), factory)
				continue
			}

			drugName := toString(d["name"])
			spec := toString(d["format"])
			drugID := int(toFloat(d["productId"]))
			storeID := 0
			storeName := ""
			minBuyNum := 1

			// Extract store info from inventoryList if available
			if invList, ok := d["inventoryList"].([]interface{}); ok && len(invList) > 0 {
				if inv, ok := invList[0].(map[string]interface{}); ok {
					storeName = toString(inv["deliveryTime"])
					if mbn := int(toFloat(inv["addCartQuantity"])); mbn > 0 {
						minBuyNum = mbn
					}
				}
			}

			items = append(items, platform.DrugResult{
				Platform:  "jyjt",
				Barcode:   toString(d["barCode"]),
				DrugName:  drugName,
				Factory:   factory,
				Spec:      spec,
				Price:     price,
				StoreID:   storeID,
				StoreName: storeName,
				DrugID:    drugID,
				MinBuyNum:  minBuyNum,
				Inventory:  int(toFloat(d["inventory"])),
				CanSaleNum: int(toFloat(d["canSaleNum"])),
				ValidDate:  toString(d["validDate"]),
			})
		}

		if len(items) > 0 {
			log.Printf("✅ [jyjt] Found %d items for %s", len(items), sku.Name)
			break
		}
		log.Printf("📋 [jyjt] No items matched for manufacturer=%s, continuing...", company)
	}

	if len(items) == 0 {
		return &platform.CrawlResult{Platform: "jyjt", Status: 3, Count: 0, Error: "no results found"}, nil
	}

	return &platform.CrawlResult{
		Platform: "jyjt",
		Status:   2,
		Count:    len(items),
		Items:    items,
	}, nil
}

func (c *JYJTClient) get(urlStr string, params url.Values) ([]byte, error) {
	req, err := http.NewRequest("GET", urlStr+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func toFloat(v interface{}) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case json.Number:
		f, _ := val.Float64()
		return f
	case string:
		var f float64
		fmt.Sscanf(val, "%f", &f)
		return f
	default:
		return 0
	}
}

func toString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
