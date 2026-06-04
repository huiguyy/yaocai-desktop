package ykx

import (
	"encoding/json"
	"fmt"
	"log"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"

	"yaocai-spider-go/internal/config"
	"yaocai-spider-go/internal/platform"
)

const YKXDefaultDomain = "https://yaokongxiao.com"

// YKXClient is the Go client for 药控销 platform
type YKXClient struct {
	CookieDict map[string]interface{}
	domain     string
	client     *http.Client
	headers    map[string]string
}

// NewYKXClient creates a new YKX client
func NewYKXClient(cookieDict map[string]interface{}) *YKXClient {
	jar, _ := cookiejar.New(nil)
	domain := YKXDefaultDomain
	if cookieDict != nil {
		if d, ok := cookieDict["domain"].(string); ok && d != "" {
			domain = d
		}
	}
	headers := map[string]string{
		"Accept":          "application/json, text/plain, */*",
		"Accept-Language":  "zh-CN,zh;q=0.9,en;q=0.8",
		"Content-Type":    "application/json",
		"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
	}
	for k, v := range cookieDict {
		if k == "domain" {
			continue
		}
		headers[k] = fmt.Sprintf("%v", v)
	}
	return &YKXClient{
		CookieDict: cookieDict,
		domain:     domain,
		client: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
		},
		headers: headers,
	}
}

func (c *YKXClient) Name() string { return "药控销" }
func (c *YKXClient) Code() string { return "ykx" }

func (c *YKXClient) Login(username, password string) (map[string]string, error) {
	return nil, fmt.Errorf("YKX login requires browser automation")
}

func (c *YKXClient) SetToken(token string) {}

// SearchDrugs implements the Platform interface
func (c *YKXClient) SearchDrugs(sku platform.DrugSKU, hasTraceCode bool) (*platform.CrawlResult, error) {
	if c.CookieDict == nil || len(c.CookieDict) == 0 {
		return nil, fmt.Errorf("YKX: not authenticated, cookie is empty")
	}

	searchURL := c.domain + "/api/front/product/list"

	// Search normal products
	params := url.Values{
		"barcode": {sku.Barcode},
		"limit":   {"1000"},
		"source":  {"yaocai"},
	}

	body, err := c.get(searchURL, params)
	if err != nil {
		return &platform.CrawlResult{Platform: "ykx", Status: 3, Error: err.Error()}, nil
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		log.Printf("❌ [ykx] JSON parse error: %v", err)
		return &platform.CrawlResult{Platform: "ykx", Status: 3, Error: "parse error"}, nil
	}
	log.Printf("📋 [ykx] search API: code=%v, data_type=%T", resp["code"], resp["data"])

	codeVal, _ := resp["code"].(float64)
	if int(codeVal) != 200 {
		return &platform.CrawlResult{Platform: "ykx", Status: 3, Error: fmt.Sprintf("API error: code=%.0f", codeVal)}, nil
	}

	var items []platform.DrugResult
	if dataField, ok := resp["data"].([]interface{}); ok {
		for _, item := range dataField {
			d, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			price := toFloat(d["price"])
			if price <= 0 || price >= config.GetConfig().PriceAnomalyThreshold {
				continue
			}

			// Extract tags
			var tags []string
			if tagList, ok := d["tags"].([]interface{}); ok {
				for _, t := range tagList {
					tags = append(tags, fmt.Sprintf("%v", t))
				}
			}

			items = append(items, platform.DrugResult{
				Platform:     "ykx",
				Barcode:      sku.Barcode,
				DrugName:     toString(d["productName"]),
				Factory:      toString(d["manufacturer"]),
				Spec:         toString(d["specification"]),
				Price:        price,
				StoreID:      int(toFloat(d["merId"])),
				StoreName:    toString(d["merName"]),
				DrugID:       int(toFloat(d["productId"])),
				MinBuyNum:    int(toFloat(d["minBuyNum"])),
				HasTraceCode: toBool(d["traceabilityCode"]),
				Tags:         tags,
			})
		}
	}

	// Also search flash sale (seckill) products
	c.searchSeckill(sku, &items)

	status := 2
	if len(items) == 0 {
		status = 3
	}

	return &platform.CrawlResult{
		Platform: "ykx",
		Status:   status,
		Count:    len(items),
		Items:    items,
	}, nil
}

// searchSeckill searches flash sale products
func (c *YKXClient) searchSeckill(sku platform.DrugSKU, items *[]platform.DrugResult) {
	// Get seckill time info
	timeURL := c.domain + "/api/front/seckill/activity/time/info"
	body, err := c.get(timeURL, nil)
	if err != nil {
		return
	}

	var timeResp map[string]interface{}
	if err := json.Unmarshal(body, &timeResp); err != nil {
		return
	}

	codeVal, _ := timeResp["code"].(float64)
	if int(codeVal) != 200 {
		return
	}

	dataList, _ := timeResp["data"].([]interface{})
	var timeParam map[string]interface{}
	for _, item := range dataList {
		if m, ok := item.(map[string]interface{}); ok {
			if status, ok := m["status"].(float64); ok && int(status) == 1 {
				timeParam = m
				break
			}
		}
	}
	if timeParam == nil {
		return
	}

	// Search seckill products
	seckillURL := c.domain + "/api/front/seckill/product/list"
	params := url.Values{
		"barcode":   {sku.Barcode},
		"limit":     {"1000"},
		"source":    {"yaocai"},
		"date":      {toString(timeParam["date"])},
		"startTime": {toString(timeParam["startTime"])},
		"endTime":   {toString(timeParam["endTime"])},
	}

	body, err = c.get(seckillURL, params)
	if err != nil {
		return
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return
	}

	codeVal, _ = resp["code"].(float64)
	if int(codeVal) != 200 {
		return
	}

	if dataField, ok := resp["data"].([]interface{}); ok {
		for _, item := range dataField {
			d, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			price := toFloat(d["price"])
			if price <= 0 || price >= config.GetConfig().PriceAnomalyThreshold {
				continue
			}

			var tags []string
			tags = append(tags, "秒杀")

			*items = append(*items, platform.DrugResult{
				Platform:     "ykx",
				Barcode:      sku.Barcode,
				DrugName:     toString(d["productName"]),
				Factory:      toString(d["manufacturer"]),
				Spec:         toString(d["specification"]),
				Price:        price,
				ActivityPrice: toFloat(d["seckillPrice"]),
				StoreID:      int(toFloat(d["merId"])),
				StoreName:    toString(d["merName"]),
				DrugID:       int(toFloat(d["productId"])),
				MinBuyNum:    int(toFloat(d["minBuyNum"])),
				HasTraceCode: toBool(d["traceabilityCode"]),
				Tags:         tags,
			})
		}
	}
}

func (c *YKXClient) get(urlStr string, params url.Values) ([]byte, error) {
	fullURL := urlStr
	if params != nil {
		fullURL = urlStr + "?" + params.Encode()
	}
	req, err := http.NewRequest("GET", fullURL, nil)
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
	if f, ok := v.(float64); ok {
		return fmt.Sprintf("%.0f", f)
	}
	return ""
}

func toBool(v interface{}) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	if f, ok := v.(float64); ok {
		return f != 0
	}
	return false
}
