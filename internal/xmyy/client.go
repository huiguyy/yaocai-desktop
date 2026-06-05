package xmyy

import (
	"encoding/json"
	"fmt"
	"log"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"yaocai-spider-go/internal/config"
	"yaocai-spider-go/internal/platform"
)

const XMYYDomain = "https://mall.xmyc.com.cn"

// XMYYClient is the Go client for 熊猫药药 platform
type XMYYClient struct {
	token  string
	client *http.Client
}

// NewXMYYClient creates a new XMYY client
func NewXMYYClient(token string) *XMYYClient {
	jar, _ := cookiejar.New(nil)
	return &XMYYClient{
		token: token,
		client: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
		},
	}
}

func (c *XMYYClient) Name() string { return "熊猫药药" }
func (c *XMYYClient) Code() string { return "xmyy" }

func (c *XMYYClient) Login(username, password string) (map[string]string, error) {
	return c.LoginViaBrowser(username, password)
}

func (c *XMYYClient) SetToken(token string) { c.token = token }

func (c *XMYYClient) buildHeaders() map[string]string {
	return map[string]string{
		"Accept":          "application/json, text/plain, */*",
		"Accept-Language":  "zh-CN,zh;q=0.9,en;q=0.8",
		"Content-Type":    "application/json; charset=UTF-8",
		"Agent":           "web",
		"AppType":         "XMYY-PC",
		"Origin":          "https://www.xmyc.com.cn",
		"Referer":         "https://www.xmyc.com.cn/",
		"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
		"Authorization":   c.token,
	}
}

// SearchDrugs implements the Platform interface
func (c *XMYYClient) SearchDrugs(sku platform.DrugSKU, hasTraceCode bool) (*platform.CrawlResult, error) {
	if c.token == "" {
		return nil, fmt.Errorf("XMYY: not authenticated, token is empty")
	}

	searchURL := XMYYDomain + "/business/center/search/searchGoods"

	// Step 1: Get factory/spec aggregation
	params := url.Values{
		"pageNum":           {"1"},
		"pageSize":          {"30"},
		"keyWord":           {sku.Name},
		"stockNumCheck":     {""},
		"order":             {""},
		"promotionCheck":    {""},
		"isMedicalInsurance": {"false"},
		"_t":               {fmt.Sprintf("%d", time.Now().UnixMilli())},
	}

	body, err := c.get(searchURL, params)
	if err != nil {
		return &platform.CrawlResult{Platform: "xmyy", Status: 3, Error: err.Error()}, nil
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		log.Printf("❌ [xmyy] JSON parse error: %v", err)
		return &platform.CrawlResult{Platform: "xmyy", Status: 3, Error: "parse error"}, nil
	}
	log.Printf("📋 [xmyy] search API: code=%v, data_type=%T", resp["code"], resp["data"])

	codeVal, _ := resp["code"].(float64)
	if int(codeVal) != 200 {
		return &platform.CrawlResult{Platform: "xmyy", Status: 3, Error: fmt.Sprintf("API error: code=%.0f", codeVal)}, nil
	}

	// Extract data
	dataField, _ := resp["data"].(map[string]interface{})
	if dataField == nil {
		return &platform.CrawlResult{Platform: "xmyy", Status: 2, Count: 0}, nil
	}

	pageInfo, _ := dataField["pageInfo"].(map[string]interface{})
	if pageInfo == nil {
		return &platform.CrawlResult{Platform: "xmyy", Status: 2, Count: 0}, nil
	}

	// The list is under pageInfo.pageable.list
	pageable, _ := pageInfo["pageable"].(map[string]interface{})
	var itemList []interface{}
	if pageable != nil {
		itemList, _ = pageable["list"].([]interface{})
	}
	if itemList == nil {
		itemList, _ = pageInfo["list"].([]interface{}) // fallback
	}

	// Get aggregation for factory matching
	aggResult, _ := pageInfo["aggregationResult"].(map[string]interface{})
	if aggResult == nil && pageable != nil {
		aggResult, _ = pageable["aggregationResult"].(map[string]interface{})
	}

	if aggResult == nil {
		// No aggregation but we still have items — skip factory filter
		log.Printf("📋 [xmyy] No aggregation found, using all %d items", len(itemList))
	}

	// Factory matching
	var platformFactories []string
	if aggResult != nil {
		if facList, ok := aggResult["productionNameAgg"].([]interface{}); ok {
			for _, f := range facList {
				platformFactories = append(platformFactories, fmt.Sprintf("%v", f))
			}
		}
	}

	var matchedFactories []string
	if len(platformFactories) > 0 {
		allFactoryNames := append([]string{sku.Factory}, sku.FactoryAlias...)
		for _, inputFactory := range allFactoryNames {
			for _, pf := range platformFactories {
				if strings.Contains(strings.ToLower(pf), strings.ToLower(inputFactory)) ||
					strings.Contains(strings.ToLower(inputFactory), strings.ToLower(pf)) {
					matchedFactories = append(matchedFactories, pf)
				}
			}
			if len(matchedFactories) > 0 {
				break
			}
		}
	}

	if len(platformFactories) > 0 && len(matchedFactories) == 0 {
		log.Printf("⚠️ [xmyy] factory not matched for %s, available=%v", sku.Factory, platformFactories[:min(5, len(platformFactories))])
		return &platform.CrawlResult{Platform: "xmyy", Status: 3, Count: 0, Error: "factory not matched"}, nil
	}

	// Parse items
	var items []platform.DrugResult
	for _, item := range itemList {
		d, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		factory := toString(d["productionName"])

		// If we have matched factories, filter by them; otherwise accept all
		if len(matchedFactories) > 0 {
			matched := false
			for _, mf := range matchedFactories {
				if strings.Contains(strings.ToLower(factory), strings.ToLower(mf)) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}

		// XMYY uses salePrice or promotionPrice, not price
		priceStr := toString(d["salePrice"])
		if priceStr == "" {
			priceStr = toString(d["promotionPrice"])
		}
		price := toFloat(priceStr)
		if price <= 0 || price >= config.GetConfig().PriceAnomalyThreshold {
			continue
		}

		items = append(items, platform.DrugResult{
			Platform:  "xmyy",
			Barcode:   sku.Barcode,
			DrugName:  toString(d["goodsName"]),
			Factory:   factory,
			Spec:      toString(d["attrName"]),
			Price:     price,
			StoreID:   120,
			StoreName: "熊猫药药",
			DrugID:    int(toFloat(d["goodsId"])),
			MinBuyNum: int(toFloat(d["packageNum"])),
		})
	}

	return &platform.CrawlResult{
		Platform: "xmyy",
		Status:   2,
		Count:    len(items),
		Items:    items,
	}, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (c *XMYYClient) get(urlStr string, params url.Values) ([]byte, error) {
	req, err := http.NewRequest("GET", urlStr+"?"+params.Encode(), nil)
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
