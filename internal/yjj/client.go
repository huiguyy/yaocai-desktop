package yjj

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
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
	YJJDomain = "https://web-api.yyjzt.com"
)

// YJJClient is the Go client for 药九九 platform
type YJJClient struct {
	ZhcaiToken  string
	UserBasicID string
	CompanyID   string
	client      *http.Client
}

// NewYJJClient creates a new YJJ client with an existing token
func NewYJJClient(token, userBasicID, companyID string) *YJJClient {
	jar, _ := cookiejar.New(nil)
	return &YJJClient{
		ZhcaiToken:  strings.TrimPrefix(token, "ssr_"),
		UserBasicID: userBasicID,
		CompanyID:   companyID,
		client: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
		},
	}
}

// signAndMarshal generates signature and JSON body for YJJ API requests
// Sign algorithm: md5(zhcaiToken + json_body_string + timestamp)
// The JSON body is produced by encoding/json.Marshal (compact, sorted by struct field order)
func (c *YJJClient) signAndMarshal(data interface{}) (sign string, timestamp string, jsonBody []byte) {
	timestamp = strconv.FormatInt(time.Now().UnixMilli(), 10)
	jsonBody, _ = json.Marshal(data)
	signStr := c.ZhcaiToken + string(jsonBody) + timestamp
	hash := md5.Sum([]byte(signStr))
	return fmt.Sprintf("%x", hash), timestamp, jsonBody
}

// searchHeaders builds the headers map for YJJ search API
func (c *YJJClient) searchHeaders(params interface{}) (http.Header, []byte) {
	_s, _t, jsonBody := c.signAndMarshal(params)
	headers := http.Header{
		"Accept":                    {"application/json, text/plain, */*"},
		"Accept-Language":           {"zh-CN,zh;q=0.9,en;q=0.8"},
		"Content-Type":              {"application/json"},
		"Origin":                    {"https://yyjzt.com"},
		"Referer":                   {"https://yyjzt.com/"},
		"User-Agent":                {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36 Edg/128.0.0.0"},
		"X-Requested-With":          {"XMLHttpRequest"},
		"Sy_plat":                   {"B2B"},
		"Token_platform_client_type": {"USER"},
		"_s":                        {_s},
		"_t":                        {_t},
		"Zhcaitoken":                {c.ZhcaiToken},
	}
	return headers, jsonBody
}

// AggResult holds the result of query_search_filters
type AggResult struct {
	Code             int
	MatchedFactories []string
	MatchedSpecs     []string
}

// aggRequest is the structured request for the aggregation API
// Field order matches Python's insertion order for signature consistency
type aggRequest struct {
	PageIndex      int           `json:"pageIndex"`
	PageSize       int           `json:"pageSize"`
	PlatformType   int           `json:"platformType"`
	Keyword        string        `json:"keyword"`
	StoreIdList    []interface{} `json:"storeIdList"`
	ManufactureList []string      `json:"manufactureList,omitempty"`
	SearchNum      int           `json:"searchNum"`
	ShowSearchTips int           `json:"showSearchTips"`
	SearchAggSource string       `json:"searchAggSource"`
	SearchAggType  int           `json:"searchAggType"`
}

// searchRequest is the structured request for the drug list API
type searchRequest struct {
	PageIndex              int           `json:"pageIndex"`
	PageSize               int           `json:"pageSize"`
	PlatformType           int           `json:"platformType"`
	Keyword                string        `json:"keyword"`
	SpecsList              []string      `json:"specsList"`
	StoreIdList            []interface{} `json:"storeIdList"`
	ManufactureList        []string      `json:"manufactureList"`
	FormulationsList       []interface{} `json:"formulationsList"`
	IsZy                   bool          `json:"isZy"`
	PriceBegin             string        `json:"priceBegin"`
	PriceEnd               string        `json:"priceEnd"`
	CmsPromoteLabelTextList []interface{} `json:"cmsPromoteLabelTextList"`
	ValidateTimeList       []interface{} `json:"validateTimeList"`
	ActivityTypes          []interface{} `json:"activityTypes"`
	IsActivity             int           `json:"isActivity"`
	IsHistory              int           `json:"isHistory"`
	IsBoughtStore          int           `json:"isBoughtStore"`
	IsYiBao                int           `json:"isYiBao"`
	IsNeedAggregation      int           `json:"isNeedAggregation"`
	IsGoldenStore          int           `json:"isGoldenStore"`
	CanViewAndBuy          int           `json:"canViewAndBuy"`
	Postage                []interface{} `json:"postage"`
	FreightFilterKey       []interface{} `json:"freightFilterKey"`
	SingleFreight          bool          `json:"singleFreight"`
	IsLocalStore           int           `json:"isLocalStore"`
	PriceReductionFlag     int           `json:"priceReductionFlag"`
	SortField              int           `json:"sortField"`
	SortAsc                int           `json:"sortAsc"`
	ChufList               []interface{} `json:"chufList"`
	SearchNum              int           `json:"searchNum"`
	ShowSearchTips         int           `json:"showSearchTips"`
	TrackingCodeFlag       int           `json:"trackingCodeFlag,omitempty"`
}

// QuerySearchFilters calls the aggregation API to match factory and specs
func (c *YJJClient) QuerySearchFilters(drugName, factory, spec string) (*AggResult, error) {
	searchURL := YJJDomain + "/search/api/search/item/searchItemAggList"
	
	// Step 1: Get factory list (searchAggType=9)
	aggParams := aggRequest{
		PageIndex:       1,
		PageSize:        60,
		PlatformType:    1,
		Keyword:         drugName,
		StoreIdList:     []interface{}{},
		SearchNum:       0,
		ShowSearchTips:  0,
		SearchAggSource: "2",
		SearchAggType:   9,
	}

	headers, jsonBody := c.searchHeaders(aggParams)
	body, err := c.doPost(searchURL, jsonBody, headers)
	if err != nil {
		return nil, fmt.Errorf("agg API error: %w", err)
	}

	var aggResp struct {
		Code    int  `json:"code"`
		Success bool `json:"success"`
		Data    struct {
			ManufactureList []string `json:"manufactureList"`
			SpecsList       []string `json:"specsList"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &aggResp); err != nil {
		return nil, fmt.Errorf("agg API parse error: %w (body: %s)", err, string(body[:min(len(body), 200)]))
	}
	if aggResp.Code != 200 || !aggResp.Success {
		return nil, fmt.Errorf("agg API returned code=%d success=%v (body: %s)", aggResp.Code, aggResp.Success, string(body[:min(len(body), 200)]))
	}

	// Match factory
	matchedFactories := matchFactory(factory, aggResp.Data.ManufactureList)
	if len(matchedFactories) == 0 {
		return &AggResult{Code: 200, MatchedFactories: nil, MatchedSpecs: nil}, nil
	}

	// Step 2: Get specs with matched factories (searchAggType=8)
	time.Sleep(time.Duration(500+rand.Float64()*1000) * time.Millisecond)
	specParams := aggRequest{
		PageIndex:       1,
		PageSize:        60,
		PlatformType:    1,
		Keyword:         drugName,
		StoreIdList:     []interface{}{},
		ManufactureList: matchedFactories,
		SearchNum:       0,
		ShowSearchTips:  0,
		SearchAggSource: "2",
		SearchAggType:   8,
	}

	headers2, jsonBody2 := c.searchHeaders(specParams)
	body2, err := c.doPost(searchURL, jsonBody2, headers2)
	if err != nil {
		return nil, fmt.Errorf("spec API error: %w", err)
	}

	var specResp struct {
		Code    int  `json:"code"`
		Success bool `json:"success"`
		Data    struct {
			SpecsList []string `json:"specsList"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body2, &specResp); err != nil {
		return nil, fmt.Errorf("spec API parse error: %w", err)
	}
	if specResp.Code != 200 || !specResp.Success {
		return nil, fmt.Errorf("spec API returned code=%d success=%v", specResp.Code, specResp.Success)
	}

	matchedSpecs := matchSpecs(spec, specResp.Data.SpecsList)
	if len(matchedSpecs) == 0 && len(specResp.Data.SpecsList) > 0 {
		matchedSpecs = specResp.Data.SpecsList // Fallback: use all specs
	}

	return &AggResult{Code: 200, MatchedFactories: matchedFactories, MatchedSpecs: matchedSpecs}, nil
}

// FlexNum handles JSON numbers that may be int, float, or string
type FlexNum = interface{}

// numFloat converts a FlexNum to float64
func numFloat(v FlexNum) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	default:
		return 0
	}
}

// numInt converts a FlexNum to int
func numInt(v FlexNum) int {
	return int(numFloat(v))
}

// SearchItem represents a single drug item from search results
// Uses FlexNum for fields that may be int/float/string in different responses
type SearchItem struct {
	ItemStoreID     FlexNum       `json:"itemStoreId"`     // int or string
	ItemStoreName   string        `json:"itemStoreName"`   // 药品名称
	ItemSpecs       string        `json:"itemSpecs"`       // 规格
	ManufactureName string        `json:"manufactureName"` // 厂家名称
	ItemPrice       FlexNum       `json:"itemPrice"`       // 价格
	StoreID         FlexNum       `json:"storeId"`         // int or string
	StoreName       string        `json:"storeName"`
	SalesNum        FlexNum       `json:"salesNum"`
	ItemValidTime   string        `json:"itemValidTime"`
	ItemPicURLList  []string      `json:"itemPicUrlList"`
	TrackingCode    FlexNum       `json:"trackingCodeFlag"`
	ActivityPrice   FlexNum       `json:"activityPrice"`   // 可能为null/float
	MinBuyNum       FlexNum       `json:"minBuyNum"`
	ItemPackageunit string        `json:"itemPackageunit"`
	ItemTagList     []interface{} `json:"itemTagList"`
	BarNo           string        `json:"barNo"`           // 条形码
	BaseNo          string        `json:"baseNo"`          // 国药准字号
	ApprovalNo      string        `json:"approvalNo"`      // 批准文号
	ItemStorage     FlexNum       `json:"itemStorage"`     // 库存
	CanSaleNum      FlexNum       `json:"canSaleNum"`      // 可能为float
	IsLocalStore    FlexNum       `json:"isLocalStore"`
	IsGoldenStore   FlexNum       `json:"isGoldenStore"`
	DiscountPrice   FlexNum       `json:"discountPrice"`   // 可能为null
	BrandName       string        `json:"brandName"`       // 品牌
}

// SearchResult holds the full search response
type SearchResult struct {
	Total    int          `json:"total"`
	Items    []SearchItem `json:"items"`
	Platform string       `json:"platform"`
}

// SearchDrugItems performs the drug search on YJJ with matched factories and specs
func (c *YJJClient) SearchDrugItems(drugName string, factories, specs []string, hasTraceCode bool) (*SearchResult, error) {
	searchURL := YJJDomain + "/search/api/search/item/list"
	
	params := searchRequest{
		PageIndex:               1,
		PageSize:                999,
		PlatformType:            1,
		Keyword:                 drugName,
		SpecsList:               specs,
		StoreIdList:             []interface{}{},
		ManufactureList:         factories,
		FormulationsList:        []interface{}{},
		IsZy:                    false,
		PriceBegin:              "",
		PriceEnd:                "",
		CmsPromoteLabelTextList: []interface{}{},
		ValidateTimeList:        []interface{}{},
		ActivityTypes:           []interface{}{},
		IsActivity:              0,
		IsHistory:               0,
		IsBoughtStore:           0,
		IsYiBao:                 0,
		IsNeedAggregation:       0,
		IsGoldenStore:           0,
		CanViewAndBuy:           0,
		Postage:                 []interface{}{},
		FreightFilterKey:        []interface{}{},
		SingleFreight:           false,
		IsLocalStore:            0,
		PriceReductionFlag:      0,
		SortField:               1,
		SortAsc:                 2,
		ChufList:                []interface{}{},
		SearchNum:               1,
		ShowSearchTips:          0,
	}
	if hasTraceCode {
		params.TrackingCodeFlag = 1
	}

	headers, jsonBody := c.searchHeaders(params)
	body, err := c.doPost(searchURL, jsonBody, headers)
	if err != nil {
		return nil, fmt.Errorf("search API error: %w", err)
	}

	var resp struct {
		Code    int  `json:"code"`
		Success bool `json:"success"`
		Data    struct {
			Needslip     bool          `json:"needslip"`
			NeedslipType FlexNum       `json:"needslipType"`
			Total        FlexNum       `json:"total"` // string or int
			ItemList     []SearchItem  `json:"itemList"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("search API parse error: %w", err)
	}

	if resp.Data.Needslip && numInt(resp.Data.NeedslipType) == 2 {
		return nil, fmt.Errorf("YJJ captcha triggered (needslip type 2)")
	}

	return &SearchResult{
		Total:    numInt(resp.Data.Total),
		Items:    resp.Data.ItemList,
		Platform: "yjj",
	}, nil
}

// doPost executes a POST request with JSON body
func (c *YJJClient) doPost(url string, jsonBody []byte, headers http.Header) ([]byte, error) {
	req, err := http.NewRequest("POST", url, strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, err
	}
	req.Header = headers

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// --- Platform interface implementation ---

func (c *YJJClient) Name() string { return "药九九" }
func (c *YJJClient) Code() string { return "yjj" }

// Login placeholder — requires browser automation
func (c *YJJClient) Login(username, password string) (map[string]string, error) {
	return nil, fmt.Errorf("YJJ login requires browser automation (not yet implemented in Go)")
}

func (c *YJJClient) SetToken(token string) {
	c.ZhcaiToken = strings.TrimPrefix(token, "ssr_")
}

// SearchDrugs implements the Platform interface for YJJ
func (c *YJJClient) SearchDrugs(sku platform.DrugSKU, hasTraceCode bool) (*platform.CrawlResult, error) {
	if c.ZhcaiToken == "" {
		return nil, fmt.Errorf("YJJ: not authenticated, token is empty")
	}

	var factories, specs []string
	factoryNames := append([]string{sku.Factory}, sku.FactoryAlias...)
	for _, factory := range factoryNames {
		result, err := c.QuerySearchFilters(sku.Name, factory, sku.Spec)
		if err != nil {
			continue
		}
		if len(result.MatchedFactories) > 0 && len(result.MatchedSpecs) > 0 {
			factories = result.MatchedFactories
			specs = result.MatchedSpecs
			break
		}
	}

	if len(factories) == 0 {
		return &platform.CrawlResult{Platform: "yjj", Status: 3, Count: 0, Error: "factory not matched"}, nil
	}
	if len(specs) == 0 {
		return &platform.CrawlResult{Platform: "yjj", Status: 3, Count: 0, Error: "spec not matched"}, nil
	}

	searchResult, err := c.SearchDrugItems(sku.Name, factories, specs, hasTraceCode)
	if err != nil {
		return &platform.CrawlResult{Platform: "yjj", Status: 3, Error: err.Error()}, nil
	}

	var items []platform.DrugResult
	for _, item := range searchResult.Items {
		price := numFloat(item.ItemPrice)
		if price <= 0 || price >= config.GetConfig().PriceAnomalyThreshold {
			continue
		}

		// Extract tag names from itemTagList
		var tagNames []string
		for _, t := range item.ItemTagList {
			if m, ok := t.(map[string]interface{}); ok {
				if name, ok := m["tagName"].(string); ok {
					tagNames = append(tagNames, name)
				}
			}
		}

		imageURL := ""
		if len(item.ItemPicURLList) > 0 {
			imageURL = item.ItemPicURLList[0]
		}

		items = append(items, platform.DrugResult{
			Platform:     "yjj",
			Barcode:      sku.Barcode,
			DrugName:     item.ItemStoreName,
			Factory:      item.ManufactureName,
			Spec:         item.ItemSpecs,
			Price:        price,
			StoreID:      numInt(item.StoreID),
			StoreName:    item.StoreName,
			SalesNum:     numInt(item.SalesNum),
			ValidDate:    item.ItemValidTime,
			ImageURL:     imageURL,
			DrugID:       numInt(item.ItemStoreID),
			MinBuyNum:    numInt(item.MinBuyNum),
			Unit:         item.ItemPackageunit,
			HasTraceCode: numInt(item.TrackingCode) > 0,
			Tags:         tagNames,
		})
	}

	return &platform.CrawlResult{
		Platform: "yjj",
		Status:   2,
		Count:    len(items),
		Items:    items,
	}, nil
}

// matchFactory matches user factory name against platform factory list
func matchFactory(userFactory string, platformFactories []string) []string {
	var matched []string
	userLower := strings.ToLower(strings.TrimSpace(userFactory))
	for _, pf := range platformFactories {
		pfLower := strings.ToLower(strings.TrimSpace(pf))
		if strings.Contains(pfLower, userLower) || strings.Contains(userLower, pfLower) {
			matched = append(matched, pf)
		}
	}
	return matched
}

// matchSpecs matches user spec against platform spec list
func matchSpecs(userSpec string, platformSpecs []string) []string {
	var matched []string
	userLower := strings.ToLower(strings.TrimSpace(userSpec))
	for _, ps := range platformSpecs {
		psLower := strings.ToLower(strings.TrimSpace(ps))
		if strings.Contains(psLower, userLower) || strings.Contains(userLower, psLower) {
			matched = append(matched, ps)
		}
	}
	if len(matched) == 0 && len(platformSpecs) > 0 {
		return platformSpecs
	}
	return matched
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
