package dek

import (
	"encoding/json"
	"fmt"
	"log"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	"yaocai-spider-go/internal/config"
	"yaocai-spider-go/internal/platform"
)

const DEKDomain = "https://www.dekyykj.com"

// DEKClient is the Go client for 德尔康 platform
type DEKClient struct {
	CookieDict map[string]interface{}
	token      string
	userId     string
	entId      string
	client     *http.Client
	headers    map[string]string
}

// NewDEKClient creates a new DEK client
func NewDEKClient(cookieDict map[string]interface{}) *DEKClient {
	jar, _ := cookiejar.New(nil)
	token := ""
	userId := ""
	entId := ""
	if cookieDict != nil {
		if t, ok := cookieDict["WEBtoken"].(string); ok {
			token = t
		}
		if u, ok := cookieDict["WEBuserId"].(string); ok {
			userId = u
		}
		if e, ok := cookieDict["WEBentId"].(string); ok {
			entId = e
		}
	}
	return &DEKClient{
		CookieDict: cookieDict,
		token:      token,
		userId:     userId,
		entId:      entId,
		client: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
		},
		headers: map[string]string{
			"Accept":          "application/json, text/plain, */*",
			"Accept-Language":  "zh-CN,zh;q=0.9,en;q=0.8",
			"Authorization":   "eyJMb2dpblR5cGUiOiJNTDQiLCJBcHBDb2RlIjoiIn0=",
			"Content-Type":    "application/json;charset=UTF-8",
			"Origin":          "https://www.dekyykj.com",
			"Referer":         "https://www.dekyykj.com/",
			"ci":              "jyly",
			"Token":           token,
			"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
		},
	}
}

func (c *DEKClient) Name() string { return "德尔康" }
func (c *DEKClient) Code() string { return "dek" }

func (c *DEKClient) Login(username, password string) (map[string]string, error) {
	loginURL := DEKDomain + "/SKB2BApi/Users/UserLoginApi"
	data := map[string]interface{}{
		"Login_Id":  username,
		"Login_Pwd": password,
		"source":    "PC",
	}
	log.Printf("[dek] Login POST to %s with user=%s", loginURL, username)
	body, err := c.postJSON(loginURL, data)
	if err != nil {
		return nil, fmt.Errorf("DEK login request failed: %w", err)
	}
	log.Printf("[dek] Login raw response: %s", string(body[:min(500, len(body))]))
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("DEK login parse error: %w", err)
	}
	// DEK API returns {"success": true/false, "message": "...", "data": {...}}
	success, _ := resp["success"].(bool)
	if !success {
		msg, _ := resp["message"].(string)
		if msg == "" {
			msg, _ = resp["msg"].(string)
		}
		return nil, fmt.Errorf("DEK login failed: %s", msg)
	}
	// Extract token from response
	dataMap, _ := resp["data"].(map[string]interface{})
	token, _ := dataMap["WEBtoken"].(string)
	if token == "" {
		// Try alternative field names
		token, _ = dataMap["token"].(string)
	}
	if token == "" {
		return nil, fmt.Errorf("DEK login: empty token in response")
	}
	// Extract userId and entId if available
	userId, _ := dataMap["userId"].(string)
	entId, _ := dataMap["entId"].(string)
	if userId == "" {
		userId, _ = dataMap["Login_Id"].(string)
	}
	log.Printf("✅ [dek] Login success: user=%s token=%s...", username, token[:min(20, len(token))])
	return map[string]string{
		"WEBtoken": token,
		"userId":   userId,
		"entId":    entId,
	}, nil
}

func (c *DEKClient) SetToken(token string) {
	c.token = token
	c.headers["Token"] = token
}

// SearchDrugs implements the Platform interface
func (c *DEKClient) SearchDrugs(sku platform.DrugSKU, hasTraceCode bool) (*platform.CrawlResult, error) {
	if c.token == "" {
		return nil, fmt.Errorf("DEK: not authenticated, token is empty")
	}

	allFactory := append([]string{sku.Factory}, sku.FactoryAlias...)

	searchURL := DEKDomain + "/SKB2BApi/Goods/GetGoodsList"
	data := map[string]interface{}{
		"userId":          c.userId,
		"entId":           c.entId,
		"searchValue":     sku.Name,
		"letter":          "",
		"tags":            "",
		"CategoryId":      "",
		"Login_Id":        "",
		"fabs":            "",
		"prescriptionType": "",
		"dosageForm":      "",
		"collection":      "N",
		"history":         "N",
		"feedback":        "N",
		"isSale":          "N",
		"isKc":            "Y",
		"drugFactory":     "",
		"drugSpec":        "",
		"minPrice":        "",
		"maxPrice":        "",
		"source":          "PC",
		"pageIndex":       1,
		"pageSize":        999,
	}

	body, err := c.postJSON(searchURL, data)
	if err != nil {
		return &platform.CrawlResult{Platform: "dek", Status: 3, Error: err.Error()}, nil
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return &platform.CrawlResult{Platform: "dek", Status: 3, Error: "parse error"}, nil
	}

	success, _ := resp["success"].(bool)
	if !success {
		return &platform.CrawlResult{Platform: "dek", Status: 3, Error: fmt.Sprintf("API error: %v", resp["msg"])}, nil
	}

	// DEK response structure: { success: true, list: [{ GoodsInfo: [...], drugFactoryList: [...], drugSpecList: [...] }] }
	// Extract GoodsInfo from the first item in the list
	var allItems []platform.DrugResult

	if listField, ok := resp["list"].([]interface{}); ok {
		for _, listItem := range listField {
			listMap, ok := listItem.(map[string]interface{})
			if !ok {
				continue
			}

			// Extract factory list for matching
			var platformFactories []string
			if factoryList, ok := listMap["drugFactoryList"].([]interface{}); ok {
				for _, f := range factoryList {
					if m, ok := f.(map[string]interface{}); ok {
						if name, ok := m["DrugFactory"].(string); ok {
							platformFactories = append(platformFactories, name)
						}
					}
				}
			}

			matchedFactories := matchFactoryList(allFactory, platformFactories)

			// Extract goods
			if goodsInfo, ok := listMap["GoodsInfo"].([]interface{}); ok {
				for _, gi := range goodsInfo {
					d, ok := gi.(map[string]interface{})
					if !ok {
						continue
					}

					factory := toString(d["Drug_Factory"])

					// Factory matching
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

					// Price parsing - DEK returns Price as string
					price := toFloat(d["Price"])
					if price <= 0 || price >= config.GetConfig().PriceAnomalyThreshold {
						continue
					}

					allItems = append(allItems, platform.DrugResult{
						Platform:  "dek",
						Barcode:   sku.Barcode,
						DrugName:  toString(d["Sub_Title"]),
						Factory:   strings.TrimSpace(factory),
						Spec:      toString(d["Drug_Spec"]),
						Price:     price,
						StoreID:   122,
						StoreName: "德尔康",
						DrugID:    int(toFloat(d["Article_Id"])),
						MinBuyNum: int(toFloat(d["Min_Package"])),
					})
				}
			}
		}
	}

	log.Printf("📋 [dek] Parsed %d items for %s", len(allItems), sku.Name)

	return &platform.CrawlResult{
		Platform: "dek",
		Status:   2,
		Count:    len(allItems),
		Items:    allItems,
	}, nil
}

func (c *DEKClient) postJSON(urlStr string, data interface{}) ([]byte, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal error: %w", err)
	}
	log.Printf("[dek] POST %s body=%s", urlStr, string(jsonData[:min(200, len(jsonData))]))
	req, err := http.NewRequest("POST", urlStr, strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, fmt.Errorf("new request error: %w", err)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http do error: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body error: %w", err)
	}
	log.Printf("[dek] POST %s status=%d body=%s", urlStr, resp.StatusCode, string(body[:min(300, len(body))]))
	return body, nil
}

func matchFactoryList(inputs []string, platformList []string) []string {
	var matched []string
	for _, input := range inputs {
		for _, pf := range platformList {
			if strings.Contains(strings.ToLower(pf), strings.ToLower(input)) ||
				strings.Contains(strings.ToLower(input), strings.ToLower(pf)) {
				matched = append(matched, pf)
			}
		}
	}
	return matched
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
