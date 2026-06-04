package main

import (
	"crypto/md5"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strconv"
	"strings"
	"time"
)

const YJJDomain = "https://web-api.yyjzt.com"

func main() {
	token := flag.String("token", "", "YJJ zhcaiToken")
	flag.Parse()

	if *token == "" {
		fmt.Fprintln(os.Stderr, "Need -token")
		os.Exit(1)
	}

	zhcaiToken := strings.TrimPrefix(*token, "ssr_")
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 30 * time.Second}

	searchURL := YJJDomain + "/search/api/search/item/list"

	params := map[string]interface{}{
		"pageIndex":       1,
		"pageSize":        999,
		"platformType":    1,
		"keyword":         "阿莫西林胶囊",
		"specsList":       []string{"0.5g*10s*2板"},
		"storeIdList":     []interface{}{},
		"manufactureList": []string{"华北制药股份有限公司"},
		"formulationsList":       []interface{}{},
		"isZy":                   false,
		"priceBegin":             "",
		"priceEnd":               "",
		"cmsPromoteLabelTextList": []interface{}{},
		"validateTimeList":       []interface{}{},
		"activityTypes":          []interface{}{},
		"isActivity":             0,
		"isHistory":              0,
		"isBoughtStore":          0,
		"isYiBao":                0,
		"isNeedAggregation":      0,
		"isGoldenStore":          0,
		"canViewAndBuy":          0,
		"postage":                []interface{}{},
		"freightFilterKey":       []interface{}{},
		"singleFreight":          false,
		"isLocalStore":           0,
		"priceReductionFlag":     0,
		"sortField":              1,
		"sortAsc":                2,
		"chufList":               []interface{}{},
		"searchNum":              1,
		"showSearchTips":         0,
	}

	jsonBody, _ := json.Marshal(params)
	_t := strconv.FormatInt(time.Now().UnixMilli(), 10)
	signStr := zhcaiToken + string(jsonBody) + _t
	hash := md5.Sum([]byte(signStr))
	_s := fmt.Sprintf("%x", hash)

	req, _ := http.NewRequest("POST", searchURL, strings.NewReader(string(jsonBody)))
	req.Header = http.Header{
		"Accept":                    {"application/json, text/plain, */*"},
		"Content-Type":              {"application/json"},
		"Origin":                    {"https://yyjzt.com"},
		"Referer":                   {"https://yyjzt.com/"},
		"User-Agent":                {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"},
		"X-Requested-With":          {"XMLHttpRequest"},
		"Sy_plat":                   {"B2B"},
		"Token_platform_client_type": {"USER"},
		"_s":                        {_s},
		"_t":                        {_t},
		"Zhcaitoken":                {zhcaiToken},
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// Save to file
	os.WriteFile("/tmp/yjj_search_response.json", body, 0644)
	fmt.Fprintf(os.Stderr, "Saved %d bytes to /tmp/yjj_search_response.json\n", len(body))
	
	// Parse and print structure
	var result map[string]interface{}
	json.Unmarshal(body, &result)
	d := result["data"].(map[string]interface{})
	
	// Print key structure
	fmt.Fprintf(os.Stderr, "Data keys: %v\n", sortedKeys(d))
	
	if rl, ok := d["resultList"].([]interface{}); ok {
		fmt.Fprintf(os.Stderr, "resultList: %d items\n", len(rl))
		if len(rl) > 0 {
			if first, ok := rl[0].(map[string]interface{}); ok {
				fmt.Fprintf(os.Stderr, "resultList[0] keys: %v\n", sortedKeys(first))
			}
		}
	}
	
	if ai, ok := d["advertItemList"].([]interface{}); ok {
		fmt.Fprintf(os.Stderr, "advertItemList: %d items\n", len(ai))
		if len(ai) > 0 {
			if first, ok := ai[0].(map[string]interface{}); ok {
				fmt.Fprintf(os.Stderr, "advertItemList[0] sample: itemPrice=%v, itemStoreName=%v, manufactureName=%v\n",
					first["itemPrice"], first["itemStoreName"], first["manufactureName"])
			}
		}
	}
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple sort
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
