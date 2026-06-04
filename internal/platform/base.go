package platform

// DrugSKU represents a single drug to search for
type DrugSKU struct {
	Name         string   `json:"name"`
	Spec         string   `json:"spec"`
	Factory      string   `json:"factory"`
	Barcode      string   `json:"barcode"`
	FactoryAlias []string `json:"factory_alias,omitempty"`
}

// DrugResult represents a single drug result from a platform
type DrugResult struct {
	Platform       string   `json:"source_platform"`
	Barcode        string   `json:"barcode"`
	DrugName       string   `json:"drug_name"`
	Factory        string   `json:"factory"`
	Spec           string   `json:"spec"`
	Price          float64  `json:"price"`
	ActivityPrice  float64  `json:"activity_price,omitempty"`
	StoreID        int      `json:"store_id"`
	StoreName      string   `json:"store_name"`
	SalesNum       int      `json:"sales_num"`
	ValidDate      string   `json:"valid_date,omitempty"`
	ImageURL       string   `json:"image_url,omitempty"`
	HasTraceCode   bool     `json:"has_trace_code"`
	Tags           []string `json:"tags,omitempty"`
	MinBuyNum      int      `json:"min_buy_num"`
	Unit           string   `json:"unit"`
	DrugID         int      `json:"drug_id"`
}

// CrawlResult holds the result of a full crawl operation
type CrawlResult struct {
	Platform string       `json:"platform"`
	Status   int          `json:"status"` // 1=started, 2=success, 3=failed
	Count    int          `json:"count"`
	Items    []DrugResult `json:"items,omitempty"`
	Error    string       `json:"error,omitempty"`
}

// Platform is the interface all platform clients must implement
type Platform interface {
	// Name returns the Chinese name of the platform
	Name() string
	
	// Code returns the short code (e.g., "yjj", "ysb")
	Code() string
	
	// Login authenticates with the platform and returns token info
	Login(username, password string) (map[string]string, error)
	
	// SearchDrugs searches for drugs matching the given criteria
	SearchDrugs(sku DrugSKU, hasTraceCode bool) (*CrawlResult, error)
	
	// SetToken sets the authentication token for subsequent requests
	SetToken(token string)
}
