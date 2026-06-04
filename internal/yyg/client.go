package yyg

import (
	"crypto/md5"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	"yaocai-spider-go/internal/config"
	"yaocai-spider-go/internal/platform"
)

const YYGDomain = "https://newapi.hezongyy.com"

// YYGClient is the Go client for 药易购 platform
type YYGClient struct {
	token  string
	client *http.Client
}

// NewYYGClient creates a new YYG client
func NewYYGClient(token string) *YYGClient {
	jar, _ := cookiejar.New(nil)
	return &YYGClient{
		token: token,
		client: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
		},
	}
}

func (c *YYGClient) Name() string { return "药易购" }
func (c *YYGClient) Code() string { return "yyg" }

func (c *YYGClient) Login(username, password string) (map[string]string, error) {
	url := YYGDomain + "/users/UserLogin/login"

	// Generate fingerprint (MD5 of username+timestamp)
	h := md5.Sum([]byte(fmt.Sprintf("%s%f", username, float64(time.Now().UnixNano())/1e9)))
	fingerprint := fmt.Sprintf("%x", h)

	data := map[string]interface{}{
		"username":       username,
		"password":       password,
		"channel":        1,
		"timeout":        604800,
		"fingerprint":    fingerprint,
		"companyLimited": 0,
	}

	body, err := c.postJSON(url, data)
	if err != nil {
		return nil, fmt.Errorf("YYG login request failed: %w", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("YYG login parse error: %w", err)
	}

	code, _ := resp["code"].(string)
	if code != "000000" {
		msg, _ := resp["message"].(string)
		return nil, fmt.Errorf("YYG login failed: code=%s msg=%s", code, msg)
	}

	token, _ := resp["content"].(string)
	if token == "" {
		return nil, fmt.Errorf("YYG login: empty token in response")
	}

	log.Printf("✅ [yyg] Login success: token=%s...", token[:min(30, len(token))])
	return map[string]string{
		"token":   token,
		"isLogin": "1",
	}, nil
}

func (c *YYGClient) SetToken(token string) { c.token = token }

// generateSignParams generates sign and timestamp for YYG requests
// Algorithm: SHA1(timestamp + "qwertyuiop"), UPPERCASE
func generateSignParams() (sign, timestamp string) {
	timestamp = fmt.Sprintf("%f", float64(time.Now().UnixNano())/1e9)
	fixedString := "qwertyuiop"
	input := timestamp + fixedString
	h := sha1.Sum([]byte(input))
	sign = fmt.Sprintf("%X", h)
	return
}

func (c *YYGClient) buildHeaders() map[string]string {
	sign, timestamp := generateSignParams()
	return map[string]string{
		"Accept":         "application/json;charset=UTF-8",
		"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
		"Content-Type":   "application/json;charset=UTF-8",
		"Origin":         "https://www.hezongyy.com",
		"Referer":        "https://www.hezongyy.com/",
		"User-Agent":     "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
		"hesytoken":      c.token,
		"sign":           sign,
		"timestamp":      timestamp,
	}
}

// GetCompanyList searches for goods by manufacturer
func (c *YYGClient) GetCompanyList(sku platform.DrugSKU, hasTraceCode bool) ([]string, error) {
	url := YYGDomain + "/goods/search/onCondition"
	factoryList := append(sku.FactoryAlias, sku.Factory)
	var goodsIDs []string

	for _, factory := range factoryList {
		data := map[string]interface{}{
			"channelType":       0,
			"columnType":        0,
			"pageNumber":        1,
			"pageSize":          16,
			"keyWords":          sku.Name,
			"manufacturer":      factory,
			"searchType":        0,
			"newCategoryLabelId": -1,
		}
		if hasTraceCode {
			data["traceabilityCode"] = 1
		}

		body, err := c.postJSON(url, data)
		if err != nil {
			continue
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(body, &resp); err != nil {
			log.Printf("❌ [yyg] onCondition JSON parse error: %v", err)
			continue
		}
		log.Printf("📋 [yyg] onCondition API: resp keys=%v, content_type=%T", func() []string { ks := make([]string, 0); for k := range resp { ks = append(ks, k) }; return ks }(), resp["content"])

		content, _ := resp["content"].(map[string]interface{})
		if content == nil {
			log.Printf("⚠️ [yyg] onCondition: content is nil, body=%s", truncateString(string(body), 300))
			continue
		}
		list, _ := content["list"].([]interface{})
		for _, item := range list {
			if m, ok := item.(map[string]interface{}); ok {
				if goodsID, ok := m["goodsId"]; ok {
					goodsIDs = append(goodsIDs, fmt.Sprintf("%v0", goodsID))
				}
			}
		}
		if len(goodsIDs) > 0 {
			break
		}
	}

	// Fallback: pure keyword search without manufacturer
	if len(goodsIDs) == 0 {
		data := map[string]interface{}{
			"channelType":       0,
			"columnType":        0,
			"pageNumber":        1,
			"pageSize":          16,
			"keyWords":          sku.Name,
			"searchType":        0,
			"newCategoryLabelId": -1,
		}
		if hasTraceCode {
			data["traceabilityCode"] = 1
		}
		body, err := c.postJSON(url, data)
		if err == nil {
			var resp map[string]interface{}
			if json.Unmarshal(body, &resp) == nil {
				if content, ok := resp["content"].(map[string]interface{}); ok {
					if list, ok := content["list"].([]interface{}); ok {
						for _, item := range list {
							if m, ok := item.(map[string]interface{}); ok {
								if goodsID, ok := m["goodsId"]; ok {
									goodsIDs = append(goodsIDs, fmt.Sprintf("%v0", goodsID))
								}
							}
						}
					}
				}
			}
		}
	}

	return goodsIDs, nil
}

// SearchDrugs implements the Platform interface
func (c *YYGClient) SearchDrugs(sku platform.DrugSKU, hasTraceCode bool) (*platform.CrawlResult, error) {
	if c.token == "" {
		return nil, fmt.Errorf("YYG: not authenticated, token is empty")
	}

	goodsIDs, err := c.GetCompanyList(sku, hasTraceCode)
	if err != nil || len(goodsIDs) == 0 {
		log.Printf("⚠️ [yyg] no goods found for %s", sku.Name)
		return &platform.CrawlResult{Platform: "yyg", Status: 3, Count: 0, Error: "no goods found"}, nil
	}
	log.Printf("🔍 [yyg] Extracted %d goodsIds, first 5: %v", len(goodsIDs), firstN(goodsIDs, 5))

	// Search with goods IDs (must use goodsIdList, not goodsIds!)
	searchURL := YYGDomain + "/goods/goods/listNormal"
	data := map[string]interface{}{
		"goodsIdList": goodsIDs,
		"limitSize":   999,
		"limitStart":  0,
	}
	dataJSON, _ := json.Marshal(data)
	log.Printf("🔍 [yyg] listNormal request: %s", truncateString(string(dataJSON), 300))
	if hasTraceCode {
		data["traceabilityCode"] = 1
	}

	body, err := c.postJSON(searchURL, data)
	if err != nil {
		return &platform.CrawlResult{Platform: "yyg", Status: 3, Error: err.Error()}, nil
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		log.Printf("❌ [yyg] listNormal JSON parse error: %v", err)
		return &platform.CrawlResult{Platform: "yyg", Status: 3, Error: "parse error"}, nil
	}
	log.Printf("📋 [yyg] listNormal API: resp keys=%v, content_type=%T", func() []string { ks := make([]string, 0); for k := range resp { ks = append(ks, k) }; return ks }(), resp["content"])
	log.Printf("🔍 [yyg] listNormal raw response: %s", truncateString(string(body), 500))

	var items []platform.DrugResult
	if content, ok := resp["content"].([]interface{}); ok {
		for _, item := range content {
			d, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			price := toFloat(d["sellingPrice"])
			if price <= 0 || price >= config.GetConfig().PriceAnomalyThreshold {
				continue
			}
			items = append(items, platform.DrugResult{
				Platform:  "yyg",
				Barcode:   sku.Barcode,
				DrugName:  toString(d["name"]),
				Factory:   toString(d["manufacturerName"]),
				Spec:      toString(d["specification"]),
				Price:     price,
				StoreID:   119,
				StoreName: "药易购",
				DrugID:    int(toFloat(d["id"])),
				MinBuyNum: int(toFloat(d["minimum"])),
				Unit:      toString(d["unit"]),
			})
		}
	}

	return &platform.CrawlResult{
		Platform: "yyg",
		Status:   2,
		Count:    len(items),
		Items:    items,
	}, nil
}

func (c *YYGClient) postJSON(urlStr string, data interface{}) ([]byte, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", urlStr, strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, err
	}
	for k, v := range c.buildHeaders() {
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

func firstN(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
