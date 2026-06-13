package ysb

import (
	"bytes"
	"crypto/aes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"io"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"sort"
	"strconv"
	"strings"
	"time"

	"yaocai-spider-go/internal/crypto"
	"yaocai-spider-go/internal/platform"
)

const (
	Domain     = "https://dian.ysbang.cn"
	Version    = "6.0.0"
	DecryptKey = "69091A0C978D8060" // search response decrypt key
	OKey       = "1AA00F7BB06A4E25" // 'o' param encrypt key
)

// YSBClient is the Go client for 药师帮 platform
type YSBClient struct {
	Token    string
	TimeDiff int64 // seconds diff between local and server
	client   *http.Client
	headers  map[string]string
}

// NewYSBClient creates a new YSB client
func NewYSBClient() *YSBClient {
	jar, _ := cookiejar.New(nil)
	return &YSBClient{
		client: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
		},
		headers: map[string]string{
			"Accept":          "*/*",
			"Accept-Language":  "zh-CN,zh;q=0.9,en;q=0.8",
			"Connection":      "keep-alive",
			"Content-Type":    "application/json",
			"Host":            "dian.ysbang.cn",
			"Origin":          "https://dian.ysbang.cn",
			"Referer":         "https://dian.ysbang.cn/",
			"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36",
		},
	}
}

// Login placeholder — requires browser automation, not yet implemented in Go
// The actual browser login will be implemented in login_browser.go (build tag: browser)
func (c *YSBClient) loginWithBrowser(username, password string) error {
	return fmt.Errorf("login not yet implemented - will use go-rod")
}

// --- Crypto helpers (ported from Python) ---

// O function — generates ex1 parameter
func O(e string) string {
	t := []int{9, 5, 2, 7}
	n := digits(e)
	r := (7 * sum(n)) % 10
	a := make([]int, len(n))
	o := 0
	for i := range n {
		a[i] = (n[i] + t[o]) % 10
		o = (o + 1) % len(t)
	}
	c := len(t) % len(n)
	s := make([]int, len(a)+1)
	for u := 0; u < c; u++ {
		s[u] = a[len(a)-u-1]
	}
	s[c] = r
	for idx := c + 1; idx < len(a)+1; idx++ {
		s[idx] = a[len(a)-idx]
	}
	return P(joinInts(s))
}

func P(e string) string {
	var result []byte
	n := makeBase36()
	val, _ := strconv.ParseInt(e, 10, 64)
	for val > 0 {
		r := val % 36
		result = append([]byte{n[r]}, result...)
		val /= 36
	}
	return string(result)
}

func (c *YSBClient) GetEx1() string {
	ts := time.Now().UnixMilli() - c.TimeDiff*1000
	return O(strconv.FormatInt(ts, 10))
}

func (c *YSBClient) GetEx(urlHash string) string {
	ct := c.CurTime()
	return fmt.Sprintf("2025-8-15 18:31 %s 04-09 10:00:02 %s", urlHash, ct)
}

func (c *YSBClient) CurTime() string {
	t := time.Now().Add(-time.Duration(c.TimeDiff) * time.Second)
	return t.Format("01-02 15:04:05")
}

func (c *YSBClient) GetO() (string, error) {
	ts := time.Now().UnixMilli() - c.TimeDiff*1000
	return crypto.GenerateOParam(ts)
}

// --- Search API ---

// SearchFiltersResult holds the result of filter query
type SearchFiltersResult struct {
	Code            int
	MatchedFactories []string
	MatchedSpecs    []string
}

// QuerySearchFilters queries YSB search filter API to get factory and spec matches
func (c *YSBClient) QuerySearchFilters(drugName, factory, spec string, matchedFactories []string) (*SearchFiltersResult, error) {
	url := Domain + "/wholesale-drug/sales/getSearchFiltersForPc/v5400"

	buttonList := buildButtonList(matchedFactories, nil, true)

	oParam, _ := c.GetO()
	data := map[string]interface{}{
		"classify_id":                "",
		"buttonList":                 buttonList,
		"drugId":                     -1,
		"ex":                         c.GetEx("indexContent"),
		"ex1":                        c.GetEx1(),
		"factoryNames":               "",
		"onlyTcm":                    0,
		"operationtype":              1,
		"page":                       1,
		"pagesize":                   "60",
		"platform":                   "pc",
		"provider_filter":            "",
		"qualifiedLoanee":            0,
		"searchkey":                  drugName,
		"showRecentlyPurchasedFlag":  true,
		"sn":                         "",
		"specs":                      "",
		"tagId":                      "",
		"tcmExeStandardIds":          []interface{}{},
		"tcmGradeNames":              []interface{}{},
		"token":                      c.Token,
		"trafficType":                1,
		"ua":                         "Chrome119",
		"version":                    Version,
	}
	_ = oParam // not needed for filter query

	resp, err := c.postJSON(url, data)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	code := getRespCode(resp)
	log.Printf("📋 [ysb] getSearchFilters API: code=%s, data_type=%T", code, resp["data"])
	if code != "40001" {
		if code == "40020" {
			return &SearchFiltersResult{Code: 401}, nil
		}
		return &SearchFiltersResult{Code: 0}, fmt.Errorf("API error: code=%s msg=%v", code, resp["message"])
	}

	dataField, ok := resp["data"].(map[string]interface{})
	if !ok {
		return &SearchFiltersResult{Code: 200}, nil
	}

	filters, ok := dataField["flattenFilters"].([]interface{})
	if !ok || len(filters) < 3 {
		return &SearchFiltersResult{Code: 200}, nil
	}

	// Extract factories from filters[1]
	var platformFactories []string
	if fl, ok := filters[1].(map[string]interface{}); ok {
		if lst, ok := fl["list"].([]interface{}); ok {
			for _, item := range lst {
				if m, ok := item.(map[string]interface{}); ok {
					if v, ok := m["value"].(string); ok {
						platformFactories = append(platformFactories, v)
					}
				}
			}
		}
	}

	// Extract specs from filters[2]
	var platformSpecs []string
	var specValues []string
	if fl, ok := filters[2].(map[string]interface{}); ok {
		if lst, ok := fl["list"].([]interface{}); ok {
			for _, item := range lst {
				if m, ok := item.(map[string]interface{}); ok {
					if name, ok := m["name"].(string); ok {
						platformSpecs = append(platformSpecs, name)
					}
					if val, ok := m["value"].(string); ok {
						specValues = append(specValues, val)
					}
				}
			}
		}
	}

	// If no matched factories provided, do factory matching
	if matchedFactories == nil {
		matched := matchDrugFactory(factory, platformFactories)
		if len(matched) == 0 {
			return &SearchFiltersResult{Code: 200, MatchedFactories: nil}, nil
		}
		// Recursive call with matched factories
		return c.QuerySearchFilters(drugName, factory, spec, matched)
	}

	// Match specs
	matchedSpecs := matchDrugSpecs(spec, platformSpecs, specValues)
	if len(matchedSpecs) == 0 && len(specValues) > 0 {
		// Fallback: use all specs
		matchedSpecs = specValues
	}

	return &SearchFiltersResult{
		Code:            200,
		MatchedFactories: matchedFactories,
		MatchedSpecs:    matchedSpecs,
	}, nil
}

// SearchDrugResult holds the result of drug search
type SearchDrugResult struct {
	Success bool
	Data    map[string]interface{}
}

// SearchDrug searches for drugs on YSB
func (c *YSBClient) SearchDrug(searchKey string, factories, specs []string, page, pageSize int) (*SearchDrugResult, error) {
	// First get filters
	factory := ""
	if len(factories) > 0 {
		factory = factories[0]
	}
	spec := ""
	if len(specs) > 0 {
		spec = specs[0]
	}

	filterResult, err := c.QuerySearchFilters(searchKey, factory, spec, nil)
	if err != nil {
		return nil, err
	}
	if filterResult.Code == 401 {
		return &SearchDrugResult{Success: false}, nil
	}
	if filterResult.Code != 200 || len(filterResult.MatchedFactories) == 0 {
		return &SearchDrugResult{Success: true}, nil
	}

	// Build search request
	url := Domain + "/wholesale-drug/sales/getWholesaleList/v4270"
	buttonList := buildButtonList(filterResult.MatchedFactories, filterResult.MatchedSpecs, false)

	oParam, _ := c.GetO()
	data := map[string]interface{}{
		"activityTypes":              []interface{}{},
		"buttonList":                 buttonList,
		"classify_id":                "",
		"deliverFloor":               0,
		"drugId":                     -1,
		"ex":                         c.GetEx("indexContent"),
		"ex1":                        c.GetEx1(),
		"factoryNames":               "",
		"o":                          oParam,
		"owRecentlyPurchased":        false,
		"onlySimpleLoan":             0,
		"onlyTcm":                    0,
		"operationtype":              1,
		"page":                       page,
		"pagesize":                   fmt.Sprintf("%d", pageSize),
		"platform":                   "pc",
		"provider_filter":            "",
		"purchaseLimitFloor":         0,
		"qualifiedLoanee":            0,
		"searchkey":                  searchKey,
		"showRecentlyPurchasedFlag":  true,
		"sn":                         "",
		"specs":                      "",
		"tagId":                      "",
		"token":                      c.Token,
		"trafficType":                1,
		"ua":                         "Chrome86",
		"version":                    Version,
	}

	time.Sleep(time.Duration(rand.Float64()*1000) * time.Millisecond)

	resp, err := c.postJSON(url, data)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}

	code := getRespCode(resp)
	log.Printf("📋 [ysb] searchDrug API: code=%s, data_type=%T", code, resp["data"])
	if code == "40001" {
		resultData := resp["data"]
		if dm, ok := resultData.(map[string]interface{}); ok {
			if oData, ok := dm["o"]; ok {
				switch v := oData.(type) {
				case string:
					decrypted, err := crypto.DecryptAESECB(v, DecryptKey)
					if err != nil {
						return nil, fmt.Errorf("decrypt failed: %w", err)
					}
					log.Printf("📋 [ysb] decrypted data keys: %v", sortedMapKeys(decrypted))
					return &SearchDrugResult{Success: true, Data: decrypted}, nil
				case map[string]interface{}:
					return &SearchDrugResult{Success: true, Data: v}, nil
				}
			}
		}
		return &SearchDrugResult{Success: true, Data: make(map[string]interface{})}, nil
	}

	if code == "40020" {
		return &SearchDrugResult{Success: false}, nil // need re-login
	}

	return nil, fmt.Errorf("search API error: code=%s", code)
}

// --- HTTP helpers ---

func (c *YSBClient) postJSON(url string, data map[string]interface{}) (map[string]interface{}, error) {
	body, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
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

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("JSON parse error: %w, body: %s", err, string(respBody[:min(len(respBody), 200)]))
	}

	return result, nil
}

// --- Helper functions ---

func buildButtonList(factories, specs []string, filterOnly bool) []map[string]interface{} {
	buttonList := []map[string]interface{}{
		{"key": "deliveryFloor", "valueStatus": []map[string]interface{}{{"status": 1, "value": "0"}}},
		{"key": "validMonth", "valueStatus": []map[string]interface{}{{"status": 1, "value": "0"}}},
	}

	if !filterOnly {
		buttonList = append(buttonList, map[string]interface{}{
			"key": "purchaseLimitFloor", "valueStatus": []map[string]interface{}{{"status": 1, "value": "0"}},
		})
	}

	if len(factories) > 0 {
		factoryStatus := make([]map[string]interface{}, len(factories))
		for i, f := range factories {
			factoryStatus[i] = map[string]interface{}{"status": 1, "value": f}
		}
		buttonList = append(buttonList, map[string]interface{}{
			"key":         "factory",
			"valueStatus": factoryStatus,
		})
	}

	if len(specs) > 0 {
		specStatus := make([]map[string]interface{}, len(specs))
		for i, s := range specs {
			specStatus[i] = map[string]interface{}{"status": 1, "value": s}
		}
		buttonList = append(buttonList, map[string]interface{}{
			"key":         "spec",
			"valueStatus": specStatus,
		})
	}

	return buttonList
}

// matchDrugFactory matches input factory name against platform factory list
// Simplified version — full fuzzy matching TBD
func matchDrugFactory(input string, platformList []string) []string {
	var matched []string
	input = strings.TrimSpace(input)
	for _, pf := range platformList {
		if strings.Contains(pf, input) || strings.Contains(input, pf) {
			matched = append(matched, pf)
		}
	}
	return matched
}

// matchDrugSpecs matches input spec against platform spec list, returns matched spec values
func matchDrugSpecs(input string, specNames, specValues []string) []string {
	var matched []string
	input = strings.TrimSpace(input)
	for i, name := range specNames {
		if strings.Contains(name, input) || strings.Contains(input, name) {
			if i < len(specValues) {
				matched = append(matched, specValues[i])
			}
		}
	}
	return matched
}

// Math/crypto utility functions

func digits(s string) []int {
	var result []int
	for _, c := range s {
		if c >= '0' && c <= '9' {
			result = append(result, int(c-'0'))
		}
	}
	return result
}

func sum(nums []int) int {
	total := 0
	for _, n := range nums {
		total += n
	}
	return total
}

func joinInts(nums []int) string {
	var sb strings.Builder
	for _, n := range nums {
		sb.WriteString(strconv.Itoa(n))
	}
	return sb.String()
}

func makeBase36() []byte {
	n := make([]byte, 36)
	for i := 0; i < 36; i++ {
		if i <= 9 {
			n[i] = byte('0' + i)
		} else {
			n[i] = byte('a' + i - 10)
		}
	}
	return n
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// sortedMapKeys returns sorted keys of a map for logging
func sortedMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// getRespCode extracts the response code as a string (handles both string and float64)
func getRespCode(resp map[string]interface{}) string {
	if code, ok := resp["code"].(string); ok {
		return code
	}
	if code, ok := resp["code"].(float64); ok {
		return fmt.Sprintf("%.0f", code)
	}
	return ""
}

// PriceToken parsing (protobuf-like binary format)
// This is used to extract actual prices from YSB's encoded price tokens

// PriceTokenField represents a parsed protobuf field
type PriceTokenField struct {
	FieldNum int
	Value    interface{} // int64 for varint, []byte for length-delimited
}

// ParsePriceToken decodes a base64-encoded priceToken (protobuf wire format)
func ParsePriceToken(b64 string) ([]PriceTokenField, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}

	var fields []PriceTokenField
	i := 0
	for i < len(raw) {
		if i >= len(raw) {
			break
		}
		b := raw[i]
		fieldNum := int(b >> 3)
		wireType := int(b & 0x7)
		i++

		switch wireType {
		case 0: // varint
			var val int64
			shift := 0
			for i < len(raw) && raw[i]&0x80 != 0 {
				val |= int64(raw[i]&0x7F) << shift
				shift += 7
				i++
			}
			if i < len(raw) {
				val |= int64(raw[i]&0x7F) << shift
				i++
			}
			fields = append(fields, PriceTokenField{FieldNum: fieldNum, Value: val})

		case 2: // length-delimited
			var length int64
			shift := 0
			for i < len(raw) && raw[i]&0x80 != 0 {
				length |= int64(raw[i]&0x7F) << shift
				shift += 7
				i++
			}
			if i < len(raw) {
				length |= int64(raw[i]&0x7F) << shift
				i++
			}
			data := raw[i : i+int(length)]
			i += int(length)
			fields = append(fields, PriceTokenField{FieldNum: fieldNum, Value: data})

		default:
			// Skip unknown wire types
			return fields, fmt.Errorf("unsupported wire type %d at field %d", wireType, fieldNum)
		}
	}

	return fields, nil
}

// ExtractPriceFromToken extracts the best price from a priceToken
// field_7 is typically the unified display price (most stable)
func ExtractPriceFromToken(b64 string) (float64, error) {
	fields, err := ParsePriceToken(b64)
	if err != nil {
		return 0, err
	}

	for _, f := range fields {
		if f.FieldNum == 7 {
			switch v := f.Value.(type) {
			case []byte:
				// Try to parse as string
				priceStr := string(v)
				// Extract numeric price
				var price float64
				_, err := fmt.Sscanf(priceStr, "%f", &price)
				if err == nil {
					return price, nil
				}
			case int64:
				return float64(v), nil
			}
		}
	}

	return 0, fmt.Errorf("price field not found in token")
}

// --- Platform interface implementation ---

// Name returns the Chinese platform name
func (c *YSBClient) Name() string { return "药师帮" }

// Code returns the short platform code
func (c *YSBClient) Code() string { return "ysb" }

// Login placeholder — requires browser automation, not yet implemented
func (c *YSBClient) Login(username, password string) (map[string]string, error) {
	return c.LoginViaBrowser(username, password)
}

// SetToken sets the auth token
func (c *YSBClient) SetToken(token string) {
	c.Token = token
}

// SearchDrugs implements the Platform interface for YSB
func (c *YSBClient) SearchDrugs(sku platform.DrugSKU, hasTraceCode bool) (*platform.CrawlResult, error) {
	if c.Token == "" {
		return nil, fmt.Errorf("YSB: not authenticated, token is empty")
	}

	// Step 1: Query search filters (factory + spec matching)
	filters, err := c.QuerySearchFilters(sku.Name, sku.Factory, sku.Spec, nil)
	if err != nil {
		return &platform.CrawlResult{Platform: "ysb", Status: 3, Error: err.Error()}, nil
	}
	if filters.Code == 401 {
		return &platform.CrawlResult{Platform: "ysb", Status: 3, Error: "unauthorized (token expired)"}, nil
	}
	if len(filters.MatchedFactories) == 0 {
		return &platform.CrawlResult{Platform: "ysb", Status: 3, Count: 0, Error: "factory not matched"}, nil
	}
	if len(filters.MatchedSpecs) == 0 {
		return &platform.CrawlResult{Platform: "ysb", Status: 3, Count: 0, Error: "spec not matched"}, nil
	}

	// Step 2: Search drugs with matched params
	searchResp, err := c.SearchDrug(sku.Name, filters.MatchedFactories, filters.MatchedSpecs, 1, 999)
	if err != nil {
		return &platform.CrawlResult{Platform: "ysb", Status: 3, Error: err.Error()}, nil
	}
	if !searchResp.Success {
		return &platform.CrawlResult{Platform: "ysb", Status: 3, Error: "search failed"}, nil
	}

	// Parse the generic map response into structured results
	var items []platform.DrugResult
	data := searchResp.Data
	if data == nil {
		return &platform.CrawlResult{Platform: "ysb", Status: 2, Count: 0}, nil
	}

	// Extract drug list from the 'wholesales' array
	// YSB returns flat drug list in 'wholesales', each item IS a drug (not a store with drugList)
	if wholesales, ok := data["wholesales"].([]interface{}); ok {
		log.Printf("📋 [ysb] wholesales count: %d", len(wholesales))
		for i, ws := range wholesales {
			d, ok := ws.(map[string]interface{})
			if !ok {
				continue
			}
			// Log first item keys
			if i == 0 {
				dKeys := make([]string, 0)
				for k := range d {
					dKeys = append(dKeys, k)
				}
				sort.Strings(dKeys)
				log.Printf("📋 [ysb] wholesales[0] keys: %v", dKeys)
			}

			// YSB field mapping:
			//   drugname → DrugName
			//   manufacturer → Factory
			//   specification → Spec
			//   price → Price (direct number)
			//   provider_name / providerName → StoreName
			//   providerId → StoreID
			//   wholesaleid → DrugID
			storeName := toString(d["provider_name"])
			if storeName == "" {
				storeName = toString(d["providerName"])
			}

			dr := platform.DrugResult{
				Platform:     "ysb",
				Barcode:      sku.Barcode,
				DrugName:     toString(d["drugname"]),
				Factory:      toString(d["manufacturer"]),
				Spec:         toString(d["specification"]),
				StoreID:      int(toFloat64(d["providerId"])),
				StoreName:    storeName,
				DrugID:       int(toFloat64(d["wholesaleid"])),
				MinBuyNum:    int(toFloat64(d["start_amount"])),
				Unit:         toString(d["unit"]),
				Price:        toFloat64(d["price"]),
				HasTraceCode: toBool(d["hasTraceCode"]),
				Inventory:    int(toFloat64(d["inventory"])),
				CanSaleNum:   int(toFloat64(d["canSaleNum"])),
				ValidDate:    toString(d["validDate"]),
			}

			// Fallback field names
			if dr.DrugName == "" {
				dr.DrugName = toString(d["druginfo_common_name"])
			}
			if dr.DrugName == "" {
				dr.DrugName = toString(d["cn_name"])
			}

			items = append(items, dr)
		}
	}

	return &platform.CrawlResult{
		Platform: "ysb",
		Status:   2,
		Count:    len(items),
		Items:    items,
	}, nil
}

// Helper functions for type conversion from map[string]interface{}
func toFloat64(v interface{}) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case int64:
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

func toBool(v interface{}) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

// Unused imports suppression
var (
	_ = aes.BlockSize
)

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
