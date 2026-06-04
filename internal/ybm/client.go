package ybm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"strings"
	"time"

	"yaocai-spider-go/internal/config"
	"yaocai-spider-go/internal/platform"
)

const (
	YBMDomain = "https://www.ybm100.com"
)

// YBMClient is the Go client for 药帮忙 platform
type YBMClient struct {
	CookieDict map[string]interface{}
	client     *http.Client
	headers    map[string]string
}

// NewYBMClient creates a new YBM client
func NewYBMClient(cookieDict map[string]interface{}) *YBMClient {
	jar, _ := cookiejar.New(nil)
	return &YBMClient{
		CookieDict: cookieDict,
		client: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
		},
		headers: map[string]string{
			"Host":            "www.ybm100.com",
			"Accept":          "application/json, text/plain, */*",
			"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
			"Content-Type":   "application/json",
			"Origin":         "https://www.ybm100.com",
			"User-Agent":     "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		},
	}
}

func (c *YBMClient) Name() string { return "药帮忙" }
func (c *YBMClient) Code() string { return "ybm" }

func (c *YBMClient) Login(username, password string) (map[string]string, error) {
	return nil, fmt.Errorf("YBM login requires browser automation (CloakBrowser)")
}

func (c *YBMClient) SetToken(token string) {}

// QuerySearchFilters queries YBM search filter API (factory + specs)
func (c *YBMClient) QuerySearchFilters(drugName string) (factories, specs []string, err error) {
	url := YBMDomain + "/new-front/search/search-categories"
	referer := YBMDomain + "/new/base/search?keyword=" + drugName

	bodyMap := map[string]interface{}{
		"queryWord":   drugName,
		"searchScene": 1,
	}
	bodyBytes, _ := json.Marshal(bodyMap)

	req, err := http.NewRequest("POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, nil, err
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Referer", referer)
	c.addCookies(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	log.Printf("📋 [ybm] search-categories API: status=%d, body_len=%d", resp.StatusCode, len(raw))

	// Check for WAF
	if strings.Contains(string(raw[:min(len(raw), 200)]), "<!DOCTYPE") {
		return nil, nil, fmt.Errorf("WAF blocked (returned HTML)")
	}

	var respJSON map[string]interface{}
	if err := json.Unmarshal(raw, &respJSON); err != nil {
		return nil, nil, fmt.Errorf("JSON parse error: %w, body=%s", err, truncate(string(raw), 200))
	}

	code := getCode(respJSON)
	if code != 200 {
		return nil, nil, fmt.Errorf("API error: code=%d", code)
	}

	dataField, _ := respJSON["data"].(map[string]interface{})
	if dataField == nil {
		return nil, nil, nil
	}

	// Extract manufacturers
	if mfgList, ok := dataField["manufacturerStats"].([]interface{}); ok {
		for _, m := range mfgList {
			if entry, ok := m.(map[string]interface{}); ok {
				if name, ok := entry["key"].(string); ok {
					factories = append(factories, name)
				}
			}
		}
	}

	// Extract specs
	if specList, ok := dataField["specStats"].([]interface{}); ok {
		for _, s := range specList {
			if entry, ok := s.(map[string]interface{}); ok {
				if name, ok := entry["key"].(string); ok {
					specs = append(specs, name)
				}
			}
		}
	}

	log.Printf("📋 [ybm] search-categories: %d factories, %d specs", len(factories), len(specs))
	return factories, specs, nil
}

// SearchDrugs searches for drugs with matched filters
func (c *YBMClient) SearchDrugs(sku platform.DrugSKU, hasTraceCode bool) (*platform.CrawlResult, error) {
	if c.CookieDict == nil || len(c.CookieDict) == 0 {
		return nil, fmt.Errorf("YBM: not authenticated, cookie is empty")
	}

	// Step 1: Get factory/spec filters
	factories, specs, err := c.QuerySearchFilters(sku.Name)
	if err != nil {
		return &platform.CrawlResult{Platform: "ybm", Status: 3, Error: fmt.Sprintf("filter query failed: %v", err)}, nil
	}

	// Match factories
	factoryNames := append([]string{sku.Factory}, sku.FactoryAlias...)
	var matchedFactories []string
	for _, f := range factoryNames {
		matchedFactories = matchFactory(f, factories)
		if len(matchedFactories) > 0 {
			break
		}
	}
	if len(matchedFactories) == 0 {
		return &platform.CrawlResult{Platform: "ybm", Status: 3, Count: 0, Error: "factory not matched"}, nil
	}

	// Match specs
	matchedSpecs := matchSpecs(sku.Spec, specs)
	if len(matchedSpecs) == 0 && len(specs) > 0 {
		matchedSpecs = specs // fallback to all specs
	}
	if len(matchedSpecs) == 0 {
		return &platform.CrawlResult{Platform: "ybm", Status: 3, Count: 0, Error: "spec not matched"}, nil
	}

	// Sleep between calls
	time.Sleep(time.Duration(500+rand.Float64()*1000) * time.Millisecond)

	// Step 2: Search products with matched filters
	manufacturersJSON, _ := json.Marshal(matchedFactories)
	specsJSON, _ := json.Marshal(matchedSpecs)

	searchBody := map[string]interface{}{
		"queryWord":    sku.Name,
		"searchScene":  1,
		"pageSize":     300,
		"pageNum":      1,
		"sortStrategy": 1,
		"type":         1,
		"tags":         "",
		"isNextPage":   0,
		"isFilter":     1,
		"manufacturers": string(manufacturersJSON),
		"specs":         string(specsJSON),
		"sid":           c.generateSID(),
		"scmId":         randomString(8),
	}
	if hasTraceCode {
		searchBody["haveTraceCode"] = `["1"]`
	}

	result, err := c.productSearch(searchBody, sku)
	if err != nil {
		return &platform.CrawlResult{Platform: "ybm", Status: 3, Error: fmt.Sprintf("search failed: %v", err)}, nil
	}
	return result, nil
}

// productSearch performs the actual product search
func (c *YBMClient) productSearch(body map[string]interface{}, sku platform.DrugSKU) (*platform.CrawlResult, error) {
	url := YBMDomain + "/new-front/search/list-products"
	referer := YBMDomain + "/new/base/search?keyword=" + sku.Name

	bodyBytes, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Referer", referer)
	c.addCookies(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	// Check WAF
	if strings.Contains(string(raw[:min(len(raw), 200)]), "<!DOCTYPE") {
		return nil, fmt.Errorf("WAF blocked")
	}

	var respJSON map[string]interface{}
	if err := json.Unmarshal(raw, &respJSON); err != nil {
		return nil, fmt.Errorf("JSON parse error: %w", err)
	}

	code := getCode(respJSON)
	if code != 200 {
		return nil, fmt.Errorf("API error: code=%d", code)
	}

	dataField, _ := respJSON["data"].(map[string]interface{})
	if dataField == nil {
		return &platform.CrawlResult{Platform: "ybm", Status: 2, Count: 0}, nil
	}

	rows, _ := dataField["rows"].([]interface{})
	log.Printf("📋 [ybm] list-products: totalCount=%v, rows=%d", dataField["totalCount"], len(rows))

	var items []platform.DrugResult
	for _, row := range rows {
		item, ok := row.(map[string]interface{})
		if !ok {
			continue
		}

		// Get product info
		var good map[string]interface{}
		if pi, ok := item["productInfo"].(map[string]interface{}); ok {
			good = pi
		} else if oi, ok := item["operationInfo"].(map[string]interface{}); ok {
			if products, ok := oi["products"].([]interface{}); ok && len(products) > 0 {
				good, _ = products[0].(map[string]interface{})
			}
		}
		if good == nil {
			continue
		}

		// Skip controlled items
		if control, _ := good["isControlAgreement"].(float64); control == 1 {
			continue
		}

		// Get price - check for group purchase (actPt) or free shipping (actPgby)
		price := toFloat64(good["fob"])
		minBuyNum := 1

		if _, hasPt := good["actPt"]; hasPt {
			if pt, ok := good["actPt"].(map[string]interface{}); ok {
				price = toFloat64(pt["assemblePrice"])
				minBuyNum = int(toFloat64(pt["skuStartNum"]))
			}
		}
		if _, hasPgby := good["actPgby"]; hasPgby {
			if pgby, ok := good["actPgby"].(map[string]interface{}); ok {
				price = toFloat64(pgby["assemblePrice"])
				minBuyNum = int(toFloat64(pgby["skuStartNum"]))
			}
		}

		if price <= 0 || price >= config.GetConfig().PriceAnomalyThreshold {
			continue
		}
		if toFloat64(good["availableQty"]) < 1 {
			continue
		}

		// Tags (trace code, scan)
		tags := []string{}
		if tagObj, ok := good["tags"].(map[string]interface{}); ok {
			if showTags, ok := tagObj["showProductTags"].([]interface{}); ok {
				for _, t := range showTags {
					if tMap, ok := t.(map[string]interface{}); ok {
						if name, ok := tMap["name"].(string); ok {
							if name == "溯" || name == "扫" {
								tags = append(tags, name)
							}
						}
					}
				}
			}
		}

		// Valid date
		validDate := toString(good["nearEffect"])
		if validDate == "" || validDate == "-" {
			validDate = toString(good["farEffect"])
		}

		// Image
		imgURL := ""
		if img, ok := good["imageUrl"].(string); ok && img != "" {
			imgURL = "https://upload.ybm100.com/ybm/product/min/" + img
		}

		// Shop & org IDs
		isSelf := toFloat64(good["isThirdCompany"]) == 0
		shopCode := toString(good["shopCode"])
		orgID := toString(good["orgId"])
		shopID := shopCode
		if !isSelf {
			shopID = orgID
		}

		items = append(items, platform.DrugResult{
			Platform:   "ybm",
			Barcode:    sku.Barcode,
			DrugName:   toString(good["originalShowName"]),
			Factory:    toString(good["manufacturer"]),
			Spec:       toString(good["spec"]),
			Price:      price,
			StoreID:    safeHashID(shopID),
			StoreName:  toString(good["shopName"]),
			SalesNum:   0,
			ValidDate:  validDate,
			ImageURL:   imgURL,
			DrugID:     int(toFloat64(good["id"])),
			MinBuyNum:  minBuyNum,
			Unit:       toString(good["productUnit"]),
			HasTraceCode: false,
			Tags:       tags,
		})
	}

	return &platform.CrawlResult{
		Platform: "ybm",
		Status:   2,
		Count:    len(items),
		Items:    items,
	}, nil
}

// Helper methods
func (c *YBMClient) addCookies(req *http.Request) {
	if c.CookieDict != nil {
		for k, v := range c.CookieDict {
			val := fmt.Sprintf("%v", v)
			// Only add ASCII-safe cookie values
			isSafe := true
			for _, ch := range val {
				if ch > 127 {
					isSafe = false
					break
				}
			}
			if isSafe {
				req.AddCookie(&http.Cookie{Name: k, Value: val})
			}
		}
	}
}

func (c *YBMClient) generateSID() string {
	now := time.Now().Format("20060102")
	merchantID := ""
	if v, ok := c.CookieDict["merchantId"]; ok {
		merchantID = fmt.Sprintf("%v", v)
	}
	return fmt.Sprintf("%s-%s-%s-4", now, merchantID, randomString(4))
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

// getCode extracts code from API response (can be float64 or string)
func getCode(resp map[string]interface{}) int {
	switch v := resp["code"].(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	case int:
		return v
	default:
		return 0
	}
}

// matchFactory matches input factory name against platform list
func matchFactory(input string, platformList []string) []string {
	var matched []string
	input = strings.ToLower(strings.TrimSpace(input))
	for _, pf := range platformList {
		pfLower := strings.ToLower(strings.TrimSpace(pf))
		if strings.Contains(pfLower, input) || strings.Contains(input, pfLower) {
			matched = append(matched, pf)
		}
	}
	return matched
}

// matchSpecs matches input spec against platform list
func matchSpecs(input string, platformList []string) []string {
	var matched []string
	input = strings.ToLower(strings.TrimSpace(input))
	for _, ps := range platformList {
		psLower := strings.ToLower(strings.TrimSpace(ps))
		if strings.Contains(psLower, input) || strings.Contains(input, psLower) {
			matched = append(matched, ps)
		}
	}
	return matched
}

func toFloat64(v interface{}) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case string:
		f, _ := strconv.ParseFloat(val, 64)
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

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// safeHashID creates a deterministic int from a string for store_id
func safeHashID(s string) int {
	h := 0
	for _, c := range s {
		h = h*31 + int(c)
	}
	if h < 0 {
		h = -h
	}
	return h % 1000000
}