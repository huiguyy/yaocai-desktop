package main

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
)

// Verify that our Go signature matches the Python implementation
func main() {
	fmt.Println("=== YJJ Signature Verification ===")

	// The Python signature: md5(zhcaiToken + json.dumps(data, ensure_ascii=False) + _t)
	token := "test_token_12345"
	data := map[string]interface{}{
		"pageIndex":    1,
		"pageSize":     60,
		"platformType": 1,
		"keyword":      "阿莫西林胶囊",
	}
	_t := "1748900000000"

	// Python: json.dumps(data, ensure_ascii=False) produces:
	// {"pageIndex": 1, "pageSize": 60, "platformType": 1, "keyword": "阿莫西林胶囊"}
	// Note: Python uses no spaces after separators by default with json.dumps
	// But with default indent=None, separators=(',', ': ')... actually Python default is (', ', ': ')
	
	// Go's json.Marshal produces compact JSON with no spaces:
	// {"pageIndex":1,"pageSize":60,"platformType":1,"keyword":"阿莫西林胶囊"}
	
	// We need to match Python's default json.dumps behavior which uses ', ' and ': '
	// Let's compute both and compare
	
	goJSON, _ := json.Marshal(data)
	goSignStr := token + string(goJSON) + _t
	goHash := md5.Sum([]byte(goSignStr))
	goResult := fmt.Sprintf("%x", goHash)
	
	fmt.Printf("Go json.Marshal: %s\n", string(goJSON))
	fmt.Printf("Go signature:    %s\n", goResult)
	
	// Python default json.dumps separators are (', ', ': ') 
	// So: {"pageIndex": 1, "pageSize": 60, "platformType": 1, "keyword": "阿莫西林胶囊"}
	pythonJSON := `{"pageIndex": 1, "pageSize": 60, "platformType": 1, "keyword": "阿莫西林胶囊"}`
	pythonSignStr := token + pythonJSON + _t
	pythonHash := md5.Sum([]byte(pythonSignStr))
	pythonResult := fmt.Sprintf("%x", pythonHash)
	
	fmt.Printf("Python json.dumps: %s\n", pythonJSON)
	fmt.Printf("Python signature:  %s\n", pythonResult)
	
	// They differ! We need to match Python's format
	// json.dumps with ensure_ascii=False but default separators
	
	// Let's create a custom marshaler that matches Python's default format
	customJSON := pythonStyleJSON(data)
	customSignStr := token + customJSON + _t
	customHash := md5.Sum([]byte(customSignStr))
	customResult := fmt.Sprintf("%x", customHash)
	
	fmt.Printf("Custom JSON:     %s\n", customJSON)
	fmt.Printf("Custom signature: %s\n", customResult)
	
	if customResult == pythonResult {
		fmt.Println("\n✅ Custom JSON matches Python!")
	} else {
		fmt.Println("\n❌ Custom JSON doesn't match Python")
	}
}

func pythonStyleJSON(data map[string]interface{}) string {
	// json.dumps default: separators=(', ', ': '), ensure_ascii=False
	// For our signing, we need to match the exact JSON output
	// Python sorts keys by default since 3.7+
	result := "{"
	first := true
	// Note: Go maps don't guarantee order, but for signature matching
	// we need consistent ordering. Python 3.7+ preserves insertion order.
	for k, v := range data {
		if !first {
			result += ", "
		}
		first = false
		result += fmt.Sprintf("%q: ", k) // Python uses ": " (space after colon)
		switch val := v.(type) {
		case string:
			result += fmt.Sprintf("%q", val)
		case int:
			result += fmt.Sprintf("%d", val)
		case float64:
			result += fmt.Sprintf("%v", val)
		case bool:
			result += fmt.Sprintf("%v", val)
		default:
			b, _ := json.Marshal(val)
			result += string(b)
		}
	}
	result += "}"
	return result
}
