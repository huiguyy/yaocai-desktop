package yyc

import (
	"crypto/aes"
	"encoding/base64"
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

const YYCDomain = "https://gateway-b2b.fangkuaiyi.com"
const YYCV = "GDLSAUO1KUMIIBCE" // AES key base for price decryption

// YYCClient is the Go client for 1药城 platform
type YYCClient struct {
	CookieDict   map[string]interface{}
	token        string
	userId       string
	factoryInfos []FactoryInfo
	client       *http.Client
	headers      map[string]string
}

// FactoryInfo stores YYC factory ID and name mapping
type FactoryInfo struct {
	ID   string
	Name string
}

// NewYYCClient creates a new YYC client
func NewYYCClient(cookieDict map[string]interface{}) *YYCClient {
	jar, _ := cookiejar.New(nil)
	token := ""
	userId := ""
	if cookieDict != nil {
		// ycgltoken is the main API token
		if t, ok := cookieDict["ycgltoken"].(string); ok {
			token = t
		}
		if u, ok := cookieDict["ycuserId"].(string); ok {
			userId = u
		}
	}
	return &YYCClient{
		CookieDict: cookieDict,
		token:      token,
		userId:     userId,
		client: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
		},
		headers: map[string]string{
			"Accept":          "application/json, text/plain, */*",
			"Accept-Language":  "zh-CN,zh;q=0.9,en;q=0.8",
			"Content-Type":    "application/x-www-form-urlencoded",
			"Origin":          "https://mall.yaoex.com",
			"Referer":         "https://mall.yaoex.com/",
			"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
			"X-Request-Agent": "Axios",
			"X-Requested-With": "XMLHttpRequest",
		},
	}
}

func (c *YYCClient) Name() string { return "1药城" }
func (c *YYCClient) Code() string { return "yyc" }

func (c *YYCClient) Login(username, password string) (map[string]string, error) {
	return c.LoginViaBrowser(username, password)
}

func (c *YYCClient) SetToken(token string) { c.token = token }

// Decrypt decrypts YYC AES-ECB encrypted price
func (c *YYCClient) Decrypt(ciphertext string) (string, error) {
	r := YYCV
	if c.userId != "" {
		r = YYCV[:10] + fmt.Sprintf("%06s", c.userId[:min(6, len(c.userId))])
	}
	block, err := aes.NewCipher([]byte(r))
	if err != nil {
		return "", err
	}
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	if len(data)%aes.BlockSize != 0 {
		return "", fmt.Errorf("ciphertext not multiple of block size")
	}
	decrypted := make([]byte, len(data))
	for i := 0; i < len(data); i += aes.BlockSize {
		block.Decrypt(decrypted[i:i+aes.BlockSize], data[i:i+aes.BlockSize])
	}
	return strings.TrimSpace(string(decrypted)), nil
}

// DecryptYYC decrypts 1药城 AES-ECB encrypted response data (legacy)
func DecryptYYC(ciphertext, key string) (string, error) {
	v := "GDLSAUO1KUMIIBCE"
	aesKey := v[:10] + fmt.Sprintf("%06s", key[:6])
	block, err := aes.NewCipher([]byte(aesKey))
	if err != nil {
		return "", err
	}
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	if len(data)%aes.BlockSize != 0 {
		return "", fmt.Errorf("ciphertext not multiple of block size")
	}
	decrypted := make([]byte, len(data))
	for i := 0; i < len(data); i += aes.BlockSize {
		block.Decrypt(decrypted[i:i+aes.BlockSize], data[i:i+aes.BlockSize])
	}
	return strings.TrimSpace(string(decrypted)), nil
}

// getRtnCode extracts the return code from YYC response (supports both "code" and "rtn_code")
func getRtnCode(resp map[string]interface{}) string {
	if code, ok := resp["code"].(string); ok {
		return code
	}
	if code, ok := resp["rtn_code"].(string); ok {
		return code
	}
	if code, ok := resp["code"].(float64); ok {
		return fmt.Sprintf("%.0f", code)
	}
	if code, ok := resp["rtn_code"].(float64); ok {
		return fmt.Sprintf("%.0f", code)
	}
	return ""
}

// GetCompany queries factory list from YYC
func (c *YYCClient) GetCompany(keyword, company string) ([]string, error) {
	u := YYCDomain + "/ycSearch/front/subsidiary/unity"
	timestamp := fmt.Sprintf("%d", time.Now().UnixMilli())

	params := url.Values{
		"traderName":       {"yaoex_pc"},
		"trader":           {"pc"},
		"closesignature":   {"yes"},
		"signature_method": {"md5"},
		"signature":        {"****"},
		"timestamp":        {timestamp},
		"token":            {c.token},
	}
	data := url.Values{
		"traderName":       {"yaoex_pc"},
		"trader":           {"pc"},
		"closesignature":   {"yes"},
		"signature_method": {"md5"},
		"signature":        {"****"},
		"timestamp":        {timestamp},
		"token":            {c.token},
		"buyerCode":        {c.userId},
		"keyword":          {keyword},
		"factoryIds":       {company},
		"isAggregate":      {"true"},
		"sellerFilterMode": {"0"},
	}

	body, err := c.postForm(u+"?"+params.Encode(), data)
	if err != nil {
		return nil, err
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	rtnCode := getRtnCode(resp)
	log.Printf("📋 [yyc] GetCompany API: rtn_code=%s, data_type=%T", rtnCode, resp["data"])

	if rtnCode != "0" {
		return nil, fmt.Errorf("YYC GetCompany error: rtn_code=%s, msg=%v", rtnCode, resp["rtn_msg"])
	}

	var factories []string
	dataField, _ := resp["data"].(map[string]interface{})
	if dataField != nil {
		if fn, ok := dataField["factoryNames"].([]interface{}); ok {
			for _, f := range fn {
				if m, ok := f.(map[string]interface{}); ok {
					if name, ok := m["factoryName"].(string); ok {
						factories = append(factories, name)
						id := fmt.Sprintf("%.0f", toFloat(m["factoryId"]))
						c.factoryInfos = append(c.factoryInfos, FactoryInfo{ID: id, Name: name})
					}
				}
			}
		}
	}
	return factories, nil
}

// SearchDrugs implements the Platform interface
func (c *YYCClient) SearchDrugs(sku platform.DrugSKU, hasTraceCode bool) (*platform.CrawlResult, error) {
	if c.token == "" {
		return nil, fmt.Errorf("YYC: not authenticated, token is empty")
	}

	// Get factory list
	compList, err := c.GetCompany(sku.Name, "")
	if err != nil {
		return &platform.CrawlResult{Platform: "yyc", Status: 3, Error: err.Error()}, nil
	}

	// Match factory
	var matchedFactories []string
	allFactoryNames := append([]string{sku.Factory}, sku.FactoryAlias...)
	for _, inputFactory := range allFactoryNames {
		for _, pf := range compList {
			if strings.Contains(strings.ToLower(pf), strings.ToLower(inputFactory)) ||
				strings.Contains(strings.ToLower(inputFactory), strings.ToLower(pf)) {
				matchedFactories = append(matchedFactories, pf)
			}
		}
		if len(matchedFactories) > 0 {
			break
		}
	}

	if len(matchedFactories) == 0 {
		log.Printf("⚠️ [yyc] factory not matched, compList=%d items, input=%s", len(compList), sku.Factory)
		return &platform.CrawlResult{Platform: "yyc", Status: 3, Count: 0, Error: "factory not matched"}, nil
	}

	log.Printf("📋 [yyc] Matched factories: %v", matchedFactories)

	// Search products - get factory IDs first
	// Get factory IDs from matched factories
	var factoryIdList []string
	for _, fi := range c.factoryInfos {
		for _, mf := range matchedFactories {
			if fi.Name == mf || strings.Contains(fi.Name, mf) {
				factoryIdList = append(factoryIdList, fi.ID)
			}
		}
	}
	log.Printf("📋 [yyc] Matched factory IDs: %v", factoryIdList)

	// Search products
	searchURL := YYCDomain + "/home/search/homeSearchList"
	timestamp2 := fmt.Sprintf("%d", time.Now().UnixMilli())
	searchParams := url.Values{
		"traderName":       {"yaoex_pc"},
		"trader":           {"pc"},
		"closesignature":   {"yes"},
		"signature_method": {"md5"},
		"signature":        {"****"},
		"timestamp":        {timestamp2},
		"token":            {c.token},
	}
	searchData := url.Values{
		"traderName":       {"yaoex_pc"},
		"trader":           {"pc"},
		"closesignature":   {"yes"},
		"signature_method": {"md5"},
		"signature":        {"****"},
		"timestamp":        {timestamp2},
		"token":            {c.token},
		"buyerCode":        {c.userId},
		"keyword":          {sku.Name},
		"factoryIds":       {strings.Join(factoryIdList, ",")},
		"specs":            {""}, // Don't pass spec - YYC spec format differs, filter in post-processing
		"isAggregate":      {"true"},
		"sellerFilterMode": {"0"},
	}

	body, err := c.postForm(searchURL+"?"+searchParams.Encode(), searchData)
	if err != nil {
		return &platform.CrawlResult{Platform: "yyc", Status: 3, Error: err.Error()}, nil
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		log.Printf("❌ [yyc] search JSON parse error: %v", err)
		return &platform.CrawlResult{Platform: "yyc", Status: 3, Error: "parse error"}, nil
	}
	rtnCode := getRtnCode(resp)
	log.Printf("📋 [yyc] search API: rtn_code=%s, data_type=%T", rtnCode, resp["data"])

	var items []platform.DrugResult
	if dataField, ok := resp["data"].(map[string]interface{}); ok {
		// YYC returns shopProducts (not list)
		var productList []interface{}
		if sp, ok := dataField["shopProducts"].([]interface{}); ok {
			productList = sp
		} else if list, ok := dataField["list"].([]interface{}); ok {
			productList = list
		}
		for _, item := range productList {
			d, ok := item.(map[string]interface{})
			if !ok {
				continue
			}

			// Decrypt price if encrypted (ends with ==)
			var price float64
			if priceStr, ok := d["price"].(string); ok && strings.HasSuffix(priceStr, "==") {
				decrypted, err := c.Decrypt(priceStr)
				if err != nil {
					log.Printf("⚠️ [yyc] price decrypt error: %v", err)
					continue
				}
				price, _ = parseFloat(decrypted)
			} else {
				price = toFloat(d["price"])
			}

			if price <= 0 || price >= config.GetConfig().PriceAnomalyThreshold {
				continue
			}

			items = append(items, platform.DrugResult{
				Platform:  "yyc",
				Barcode:   sku.Barcode,
				DrugName:  toString(d["productName"]),
				Factory:   toString(d["factoryName"]),
				Spec:      toString(d["spec"]),
				Price:     price,
				StoreID:   int(toFloat(d["vendorId"])),
				StoreName: toString(d["vendorName"]),
				DrugID:    int(toFloat(d["productId"])),
				Inventory:  int(toFloat(d["inventory"])),
				CanSaleNum: int(toFloat(d["canSaleNum"])),
				ValidDate:  toString(d["validDate"]),
			})
		}
	}

	return &platform.CrawlResult{
		Platform: "yyc",
		Status:   2,
		Count:    len(items),
		Items:    items,
	}, nil
}

func (c *YYCClient) postForm(urlStr string, data url.Values) ([]byte, error) {
	req, err := http.NewRequest("POST", urlStr, strings.NewReader(data.Encode()))
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
		f, _ := parseFloat(val)
		return f
	default:
		return 0
	}
}

func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

func toString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
