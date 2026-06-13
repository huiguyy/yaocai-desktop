package api

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"yaocai-spider-go/internal/cache"
	"yaocai-spider-go/internal/config"
	"yaocai-spider-go/internal/dek"
	"yaocai-spider-go/internal/jyjt"
	"yaocai-spider-go/internal/platform"
	"yaocai-spider-go/internal/xmyy"
	"yaocai-spider-go/internal/ybm"
	"yaocai-spider-go/internal/yjj"
	"yaocai-spider-go/internal/ykx"
	"yaocai-spider-go/internal/ysb"
	"yaocai-spider-go/internal/yyc"
	"yaocai-spider-go/internal/yyg"
)

const (
	spiderVersion  = "3.1.0"
	spiderLang     = "go"
	serverDomain  = "http://192.168.0.111:8280"
)

// PlatformConfig holds per-platform configuration from /apply_all_spider
type PlatformConfig struct {
	Cookie       json.RawMessage `json:"cookie"`
	LocalStorage string          `json:"local_storage"`
	Domain       *string         `json:"domain"`
	IsUsed       bool            `json:"is_used"`
	Enabled      bool            `json:"enabled"`
	SupplyMode   int             `json:"supply_mode"`
	// Go Spider additions
	CookieDict map[string]interface{} `json:"-"`
	Token      string                 `json:"-"`
}

// ApplyAllSpiderRequest is the full /apply_all_spider request body
type ApplyAllSpiderRequest struct {
	PlanID              int                         `json:"plan_id"`
	PriorityLowestPrice bool                        `json:"priority_lowest_price"`
	HasTraceCode        bool                        `json:"has_trace_code"`
	SkuList             []ApplySkuItem              `json:"sku_list"`
	PlatformsRaw        json.RawMessage             `json:"platforms,omitempty"` // can be dict or array
	Credentials         map[string]ApplyCredential  `json:"credentials,omitempty"`
	BackendToken         string                      `json:"backend_token,omitempty"`
	Token                string                      `json:"token,omitempty"` // Python Spider compatibility
	UseCache            bool                        `json:"use_cache,omitempty"`
	// Individual platform configs (may be at top level)
	Ysb  *PlatformConfig `json:"ysb,omitempty"`
	Ybm  *PlatformConfig `json:"ybm,omitempty"`
	Yjj  *PlatformConfig `json:"yjj,omitempty"`
	Yyc  *PlatformConfig `json:"yyc,omitempty"`
	Yyg  *PlatformConfig `json:"yyg,omitempty"`
	Xmyy *PlatformConfig `json:"xmyy,omitempty"`
	Jyjt *PlatformConfig `json:"jyjt,omitempty"`
	Dek  *PlatformConfig `json:"dek,omitempty"`
	Ykx  *PlatformConfig `json:"ykx,omitempty"`
}

// ApplySkuItem matches Python Spider's sku_list format
type ApplySkuItem struct {
	Barcode      string   `json:"barcode"`
	Name         string   `json:"name"`
	DrugName     string   `json:"drug_name"` // Python Spider uses this field
	Number       int      `json:"number"`
	Spec         string   `json:"spec"`
	Factory      string   `json:"factory"`
	FactoryAlias []string `json:"factory_alias,omitempty"`
	Validity     int      `json:"validity,omitempty"`
	ExpireDate   string   `json:"expire_date,omitempty"`
	UPC          *string  `json:"upc,omitempty"`
	Platforms    []string `json:"platforms,omitempty"` // per-SKU platform list
}

// ApplyCredential holds username/password for a platform
type ApplyCredential struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// CrawlTask tracks a running crawl task
type CrawlTask struct {
	PlanID      int
	StartTime   time.Time
	PlatformMap map[string]*PlatformConfig
	SkuList     []ApplySkuItem
	Mu          sync.Mutex
	Results     map[string]*CrawlPlatformResult // platform_code -> result
	AllDone     chan struct{}
	DoneCount   int
	TotalCount  int
	CallbackURL string // Backend callback URL
	Token       string // Backend token for callback
}

// CrawlPlatformResult holds per-platform crawl results
type CrawlPlatformResult struct {
	Platform string           `json:"platform"`
	Status   int              `json:"status"` // 1=started, 2=success, 3=failed
	Count    int              `json:"count"`
	Items    []platform.DrugResult `json:"items,omitempty"`
	Error    string           `json:"error,omitempty"`
	StartTime time.Time       `json:"-"`
	EndTime   time.Time       `json:"-"`
}

// Server is the HTTP API server compatible with Python Spider
type Server struct {
	Port         int
	serverDomain   string // Backend URL
	backendToken   string // token for Backend API
	fileServerURL string // AI server local file server for result uploads
	mu           sync.Mutex
	runningPlan  int            // currently running plan_id (0 = idle)
	task         *CrawlTask
	loginCache   map[string]map[string]interface{} // platform -> cookie_dict
	cacheOnce    sync.Once                       // ensure loadLoginCache runs only once
}

// NewServer creates a new API server
func NewServer(port int) *Server {
	return &Server{
		Port:          port,
		serverDomain:  serverDomain,
		fileServerURL: "http://192.168.0.111:8290",
	}
}

// SetServerDomain allows overriding the Backend URL
func (s *Server) SetServerDomain(domain string) {
	s.serverDomain = domain
}

// GetRunningPlanID returns the currently running plan ID (0 if idle)
func (s *Server) GetRunningPlanID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runningPlan
}

// Start starts the HTTP server
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Health check (compatible with Python Spider)
	mux.HandleFunc("/health", s.handleHealth)

	// Root
	mux.HandleFunc("/", s.handleRoot)

	// Config endpoints
	mux.HandleFunc("/config", s.handleConfig)

	// Heartbeat
	mux.HandleFunc("/heartbeat", s.handleHeartbeat)

	// Login cache
	mux.HandleFunc("/login_cache", s.handleLoginCache)
	mux.HandleFunc("/login_cache/clear", s.handleLoginCacheClear)

	// --- Main crawl endpoints (Python Spider compatible) ---

	// /apply_all_spider — Backend calls this
	mux.HandleFunc("/apply_all_spider", s.handleApplyAllSpider)

	// /get/plan/schedule — poll crawl progress
	mux.HandleFunc("/get/plan/schedule", s.handleGetPlanSchedule)

	// /plan/status — simple plan status
	mux.HandleFunc("/plan/status", s.handlePlanStatus)
	mux.HandleFunc("/platform/login", s.handlePlatformLogin)

	addr := fmt.Sprintf(":%d", s.Port)
	log.Printf("🕷️ Spider API v%s (Go) starting on %s", spiderVersion, addr)
	log.Printf("📋 Backend: %s", s.serverDomain)

	// Auto-login pure-API platforms on startup
	go s.autoLoginPureAPIPlatforms()

	// Periodic token renewal every 30 minutes for pure-API platforms
	go s.periodicRenewLoop()

	return http.ListenAndServe(addr, mux)
}

// ============= Endpoint Handlers =============

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	jsonResp(w, http.StatusOK, map[string]interface{}{
		"code": 100,
		"msg":  "success",
		"data": map[string]interface{}{
			"version": spiderVersion,
			"lang":    spiderLang,
		},
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	planID := s.GetRunningPlanID()
	jsonResp(w, http.StatusOK, map[string]interface{}{
		"code": 100,
		"msg":  "healthy",
		"data": map[string]interface{}{
			"version":         spiderVersion,
			"lang":            spiderLang,
			"running_plan_id": planID,
			"is_idle":         planID == 0,
		},
	})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := config.GetConfig()
		jsonResp(w, http.StatusOK, map[string]interface{}{
			"code": 100,
			"data": map[string]interface{}{
				"price_threshold": cfg.PriceAnomalyThreshold,
			},
		})

	case http.MethodPost:
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResp(w, http.StatusBadRequest, map[string]interface{}{"code": 400, "msg": err.Error()})
			return
		}
		key, _ := req["key"].(string)
		if key == "price_threshold" {
			if v, ok := req["value"].(float64); ok {
				_, err := config.UpdateConfig(map[string]interface{}{"price_anomaly_threshold": v})
				if err != nil {
					jsonResp(w, http.StatusInternalServerError, map[string]interface{}{"code": 500, "msg": err.Error()})
					return
				}
				jsonResp(w, http.StatusOK, map[string]interface{}{
					"code": 100,
					"msg":  "价格异常阈值已更新",
					"data": map[string]interface{}{"threshold": v},
				})
				return
			}
		}
		jsonResp(w, http.StatusOK, map[string]interface{}{
			"code": 400,
			"msg":  fmt.Sprintf("不支持的配置项: %v", key),
		})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	jsonResp(w, http.StatusOK, map[string]interface{}{
		"status":    "alive",
		"timestamp": time.Now().Unix(),
		"version":   spiderVersion,
	})
}

func (s *Server) handleLoginCache(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		lc := cache.GetCache("login_cache.json")
		jsonResp(w, http.StatusOK, map[string]interface{}{
			"code": 100,
			"msg":  "ok",
			"data": lc.Status(),
		})
	case http.MethodPost:
		// Clear cache
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResp(w, http.StatusBadRequest, map[string]interface{}{"code": 400, "msg": err.Error()})
			return
		}
		platform, _ := req["platform"].(string)
		lc := cache.GetCache("login_cache.json")
		lc.Clear(platform)
		jsonResp(w, http.StatusOK, map[string]interface{}{
			"code": 100,
			"msg":  "缓存已清除",
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleLoginCacheClear(w http.ResponseWriter, r *http.Request) {
	lc := cache.GetCache("login_cache.json")
	lc.Clear("") // clear all
	jsonResp(w, http.StatusOK, map[string]interface{}{
		"code": 100,
		"msg":  "所有缓存已清除",
	})
}

// handleApplyAllSpider implements the Python Spider compatible /apply_all_spider endpoint.
// Request format:
//
//	{
//	  "plan_id": 12,
//	  "priority_lowest_price": false,
//	  "has_trace_code": false,
//	  "sku_list": [
//	    {"barcode": "...", "name": "阿莫西林胶囊", "number": 1, "spec": "...", "factory": "..."}
//	  ],
//	  "platforms": {
//	    "yjj": {"cookie": "[...]", "is_used": true, "enabled": true},
//	    "ysb": {"cookie": "[...]", "is_used": true, "enabled": true}
//	  }
//	}
//
// Or platforms at top level:
//
//	{
//	  "plan_id": 12,
//	  "yjj": {"cookie": "[...]", ...},
//	  "ysb": {"cookie": "[...]", ...},
//	  ...
//	}
func (s *Server) handleApplyAllSpider(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if another task is running
	s.mu.Lock()
	if s.runningPlan != 0 {
		planID := s.runningPlan
		s.mu.Unlock()
		jsonResp(w, http.StatusOK, map[string]interface{}{
			"code":    1,
			"msg":    "正在执行其他任务，请稍后再试",
			"plan_id": planID,
		})
		return
	}

	var req ApplyAllSpiderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.mu.Unlock()
		jsonResp(w, http.StatusBadRequest, map[string]interface{}{"code": 400, "msg": err.Error()})
		return
	}

	// Handle self-update (plan_id=-1)
	if req.PlanID == -1 {
		s.mu.Unlock()
		s.handleSelfUpdate(w, req)
		return
	}

	if len(req.SkuList) == 0 {
		s.mu.Unlock()
		jsonResp(w, http.StatusBadRequest, map[string]interface{}{"code": 400, "msg": "sku_list is empty"})
		return
	}

	// Mark as running
	s.runningPlan = req.PlanID
	s.mu.Unlock()

	log.Printf("################################################")
	log.Printf("📦 /apply_all_spider plan_id=%d skus=%d version=%s", req.PlanID, len(req.SkuList), spiderVersion)

	// Normalize platform configs
	platformMap := s.normalizePlatformConfigs(&req)
	log.Printf("📋 Parsed platform configs: %d platforms: %v", len(platformMap), func() []string { keys := make([]string, 0); for k := range platformMap { keys = append(keys, k) }; return keys }())

	// Extract backend token from header or request body
	backendToken := r.Header.Get("Token")
	if req.BackendToken != "" {
		backendToken = req.BackendToken
	}
	if req.Token != "" {
		backendToken = req.Token // Python Spider compatibility
	}

	// Start crawl in background
	task := &CrawlTask{
		PlanID:      req.PlanID,
		StartTime:   time.Now(),
		PlatformMap: platformMap,
		SkuList:     req.SkuList,
		Results:     make(map[string]*CrawlPlatformResult),
		AllDone:     make(chan struct{}),
		CallbackURL: s.serverDomain + "/api/v1/order_inventory/best_price/",
		Token:       backendToken,
	}
	task.TotalCount = len(platformMap)

	s.mu.Lock()
	s.task = task
	s.backendToken = backendToken
	s.mu.Unlock()

	go s.runCrawl(task, req.HasTraceCode)

	jsonResp(w, http.StatusOK, map[string]interface{}{
		"code": 100,
		"msg":  "任务执行成功",
	})
}

// normalizePlatformConfigs extracts platform configs from various request formats
func (s *Server) normalizePlatformConfigs(req *ApplyAllSpiderRequest) map[string]*PlatformConfig {
	result := make(map[string]*PlatformConfig)

	// Known platform keys
	platformKeys := []string{"ysb", "ybm", "yjj", "yyc", "yyg", "xmyy", "jyjt", "dek"}

	// Parse platforms field — can be dict or array
	if len(req.PlatformsRaw) > 0 {
		// Try dict format first: {"yjj": {"enabled": true, ...}, ...}
		var dictForm map[string]*PlatformConfig
		if err := json.Unmarshal(req.PlatformsRaw, &dictForm); err == nil {
			for _, key := range platformKeys {
				if cfg, ok := dictForm[key]; ok && cfg != nil && cfg.Enabled {
					result[key] = cfg
				}
			}
		} else {
			// Try array format: ["yjj", "ysb", ...]
			var arrayForm []string
			if err := json.Unmarshal(req.PlatformsRaw, &arrayForm); err == nil {
				for _, key := range arrayForm {
					for _, pk := range platformKeys {
						if key == pk {
							result[key] = &PlatformConfig{Enabled: true}
							break
						}
					}
				}
			}
		}
	}

	// Check if individual top-level platform configs exist
	topLevel := map[string]*PlatformConfig{
		"ysb":  req.Ysb,
		"ybm":  req.Ybm,
		"yjj":  req.Yjj,
		"yyc":  req.Yyc,
		"yyg":  req.Yyg,
		"xmyy": req.Xmyy,
		"jyjt": req.Jyjt,
		"dek":  req.Dek,
		// ykx removed — internal testing platform, not included in production
	}
	for key, cfg := range topLevel {
		if _, exists := result[key]; !exists && cfg != nil && cfg.Enabled {
			result[key] = cfg
		}
	}

	// Also check per-SKU platforms field (Python Spider puts platforms in each SKU)
	if len(result) == 0 {
		for _, sku := range req.SkuList {
			for _, p := range sku.Platforms {
				for _, pk := range platformKeys {
					if p == pk {
						if _, exists := result[pk]; !exists {
							result[pk] = &PlatformConfig{Enabled: true}
						}
						break
					}
				}
			}
		}
	}

	// Fallback: if no platforms specified at all, use all platforms from login_cache
	if len(result) == 0 {
		for _, key := range platformKeys {
			result[key] = &PlatformConfig{Enabled: true}
		}
		log.Printf("📋 No platforms in request, using all %d available platforms", len(result))
	}

	// Parse cookie strings into cookie_dict for each platform
	for key, cfg := range result {
		s.parsePlatformCookie(key, cfg)
	}

	return result
}

// parsePlatformCookie parses cookie JSON string into cookie_dict map
func (s *Server) parsePlatformCookie(key string, cfg *PlatformConfig) {
	if len(cfg.Cookie) > 0 {
		var cookieList []map[string]interface{}
		if err := json.Unmarshal(cfg.Cookie, &cookieList); err == nil {
			cfg.CookieDict = make(map[string]interface{})
			for _, c := range cookieList {
				if name, ok := c["name"].(string); ok {
					cfg.CookieDict[name] = flattenValue(c["value"])
				}
			}
			// Extract token from cookie dict
			switch key {
			case "ysb":
				if t, ok := cfg.CookieDict["Token"].(string); ok {
					cfg.Token = t
				}
			case "yjj":
				if t, ok := cfg.CookieDict["yjj-token"].(string); ok {
					cfg.Token = t
				}
			case "yyg":
				if t, ok := cfg.CookieDict["yyg-token"].(string); ok {
					cfg.Token = t
				}
			}
		}
	}
}

// flattenValue converts nested JSON values (like {"value":"..."}) to simple string values.
// When login_cache.json is parsed, cookie_dict values may be nested objects instead of strings.
// This function extracts the string value from common patterns:
//   - {"value": "xxx"} -> "xxx"
//   - map[string]interface{} with single string value -> that value
//   - string -> string (unchanged)
//   - float64 -> fmt.Sprintf("%v", v) (JSON numbers)
func flattenValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case map[string]interface{}:
		// Handle {"value": "xxx"} pattern
		if inner, ok := val["value"]; ok {
			return flattenValue(inner)
		}
		// Try first string-valued key as fallback
		for _, v2 := range val {
			if s, ok := v2.(string); ok {
				return s
			}
		}
		// Last resort: JSON encode
		b, _ := json.Marshal(val)
		return string(b)
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%v", val)
	case bool:
		return fmt.Sprintf("%v", val)
	case nil:
		return ""
	default:
		b, _ := json.Marshal(val)
		return string(b)
	}
}

// flattenCookieDict flattens all values in a cookie_dict to strings
func flattenCookieDict(dict map[string]interface{}) map[string]interface{} {
	if dict == nil {
		return nil
	}
	result := make(map[string]interface{}, len(dict))
	for k, v := range dict {
		result[k] = flattenValue(v)
	}
	return result
}

// autoRenewCredentials checks and renews login credentials for pure-API platforms
func (s *Server) autoRenewCredentials(platforms []string) {
	lc := cache.GetCache("login_cache.json")
	
	// Fetch credentials from Backend API
	creds := s.fetchBackendCredentials()
	
	for _, code := range platforms {
		c, ok := creds[code]
		if !ok || c.username == "" {
			continue // No auto-login credentials for this platform
		}
		
		entry := lc.Get(code)
		if entry != nil {
			remaining := entry.TTL - (time.Now().Unix() - entry.CachedAt)
			if remaining > 1800 { // more than 30min remaining
				continue
			}
			log.Printf("🔄 [%s] Cache expiring soon (%dmin remaining), auto-renewing...", code, remaining/60)
		} else {
			log.Printf("🔄 [%s] Cache expired, auto-renewing...", code)
		}
		
		// Only auto-login for pure-API platforms (no browser/captcha required)
		var cookieDict map[string]string
		var err error
		switch code {
		case "yyg":
			client := yyg.NewYYGClient("")
			cookieDict, err = client.Login(c.username, c.password)
		case "jyjt":
			client := jyjt.NewJYJTClient(nil)
			cookieDict, err = client.Login(c.username, c.password)
		case "dek":
			client := dek.NewDEKClient(nil)
			cookieDict, err = client.Login(c.username, c.password)
		default:
			log.Printf("⚠️ [%s] No pure-API login, needs browser/captcha, skipping auto-renew", code)
			continue
		}
		
		if err != nil {
			log.Printf("❌ [%s] Auto-renew failed: %v", code, err)
			continue
		}
		
		// Update cache
		cacheMap := make(map[string]interface{})
		for k, v := range cookieDict {
			cacheMap[k] = v
		}
		lc.Set(code, cacheMap, "", c.username)
		s.mu.Lock()
		if s.loginCache == nil {
			s.loginCache = make(map[string]map[string]interface{})
		}
		s.loginCache[code] = cacheMap
		s.mu.Unlock()
		log.Printf("✅ [%s] Auto-renew success!", code)
	}
}
// periodicRenewLoop runs token renewal every N minutes for pure-API platforms
func (s *Server) periodicRenewLoop() {
	time.Sleep(20 * time.Minute) // First renewal after 20 min

	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		log.Printf("🔄 [periodic-renew] Starting periodic token renewal...")
		s.mu.Lock()
		platformList := make([]string, 0)
		for code := range s.loginCache {
			platformList = append(platformList, code)
		}
		s.mu.Unlock()
		s.autoRenewCredentials(platformList)
	}
}

// fetchBackendCredentials gets platform credentials from Backend API
func (s *Server) fetchBackendCredentials() map[string]struct{ username, password string } {
	result := make(map[string]struct{ username, password string })
	
	resp, err := http.Get(s.serverDomain + "/api/v1/spider/credentials/?token=af87045b45404b12851816cd4f6a3903")
	if err != nil {
		log.Printf("⚠️ Failed to fetch credentials from Backend: %v", err)
		return result
	}
	defer resp.Body.Close()
	
	var respData struct {
		Data map[string]struct {
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		log.Printf("⚠️ Failed to decode credentials: %v", err)
		return result
	}
	
	for code, cred := range respData.Data {
		result[code] = struct{ username, password string }{cred.Username, cred.Password}
	}
	log.Printf("📋 Fetched credentials for %d platforms from Backend", len(result))
	return result
}

// fetchPlatformCredentials tries to get platform credentials from login cache
func (s *Server) fetchPlatformCredentials(platformCode string) (string, map[string]interface{}) {
	s.cacheOnce.Do(func() {
		s.loadLoginCache()
	})
	var token string
	var cookieDict map[string]interface{}

	if cache, ok := s.loginCache[platformCode]; ok {
		cookieDict, _ = cache["cookie_dict"].(map[string]interface{})
		// Flatten nested values in cookie_dict
		cookieDict = flattenCookieDict(cookieDict)
		// Platform-specific token extraction
		switch platformCode {
		case "yjj":
			if t, ok := cookieDict["yjj-token"].(string); ok {
				token = t
			}
		case "ysb":
			if t, ok := cookieDict["Token"].(string); ok {
				token = t
			}
		case "yyc":
			if t, ok := cookieDict["ycgltoken"].(string); ok {
				token = t
			}
		case "yyg":
			if t, ok := cookieDict["token"].(string); ok {
				token = t
			}
		case "xmyy":
			if t, ok := cookieDict["HT_CACHE_TOKEN"].(string); ok {
				token = t
			}
		case "ybm":
			if t, ok := cookieDict["xyy_token"].(string); ok {
				token = t
			}
		case "dek":
			if t, ok := cookieDict["WEBtoken"].(string); ok {
				token = t
			}
		case "jyjt":
			if t, ok := cookieDict["jzyy__Shopping-Access-Token"].(string); ok {
				token = t
			}
		}
		if token != "" {
			log.Printf("📋 [%s] Got token from login_cache", platformCode)
		} else if len(cookieDict) > 0 {
			log.Printf("📋 [%s] Got cookie_dict from login_cache (%d keys)", platformCode, len(cookieDict))
		}
	} else {
		log.Printf("⚠️ [%s] No credentials found in login_cache", platformCode)
	}

	return token, cookieDict
}

// loadLoginCache reads login_cache.json from the executable's directory
func (s *Server) loadLoginCache() {
	s.loginCache = make(map[string]map[string]interface{})

	exePath, err := os.Executable()
	if err != nil {
		log.Printf("⚠️ Cannot determine exe path: %v", err)
		return
	}
	cachePath := filepath.Join(filepath.Dir(exePath), "..", "login_cache.json")

	// Also try parent dir (when exe is in go-spider/ subdirectory)
	data, err := os.ReadFile(cachePath)
	if err != nil {
		cachePath = filepath.Join(filepath.Dir(exePath), "login_cache.json")
		data, err = os.ReadFile(cachePath)
	}
	if err != nil {
		log.Printf("⚠️ Cannot read login_cache.json: %v", err)
		return
	}

	var cache map[string]map[string]interface{}
	if err := json.Unmarshal(data, &cache); err != nil {
		log.Printf("⚠️ Cannot parse login_cache.json: %v", err)
		return
	}

	s.loginCache = cache

	// Normalize: flatten nested cookie_dict values to strings
	for platform, platformCache := range cache {
		if cookieDict, ok := platformCache["cookie_dict"].(map[string]interface{}); ok {
			cache[platform]["cookie_dict"] = flattenCookieDict(cookieDict)
		}
	}

	log.Printf("📋 Loaded login cache for %d platforms", len(cache))
}

// getPlatformLoginCredentials extracts username/password from login_cache for a platform
func (s *Server) getPlatformLoginCredentials(platformCode string) (string, string) {
	s.cacheOnce.Do(func() {
		s.loadLoginCache()
	})
	cache, ok := s.loginCache[platformCode]
	if !ok {
		return "", ""
	}
	username, _ := cache["username"].(string)
	password, _ := cache["password"].(string)
	return username, password
}

// updateLoginCacheToken updates the login_cache.json with new credentials after successful auto-login
func (s *Server) updateLoginCacheToken(platformCode string, newCreds map[string]string) {
	s.cacheOnce.Do(func() {
		s.loadLoginCache()
	})

	// Update in-memory cache
	if _, ok := s.loginCache[platformCode]; !ok {
		s.loginCache[platformCode] = make(map[string]interface{})
	}
	if cookieDict, ok := s.loginCache[platformCode]["cookie_dict"].(map[string]interface{}); ok {
		for k, v := range newCreds {
			cookieDict[k] = v
		}
	} else {
		newCookieDict := make(map[string]interface{})
		for k, v := range newCreds {
			newCookieDict[k] = v
		}
		s.loginCache[platformCode]["cookie_dict"] = newCookieDict
	}

	// Write back to file
	exePath, err := os.Executable()
	if err != nil {
		log.Printf("⚠️ Cannot determine exe path for cache update: %v", err)
		return
	}
	cachePath := filepath.Join(filepath.Dir(exePath), "..", "login_cache.json")
	data, err := json.MarshalIndent(s.loginCache, "", "  ")
	if err != nil {
		log.Printf("⚠️ Cannot marshal login_cache: %v", err)
		return
	}
	if err := os.WriteFile(cachePath, data, 0644); err != nil {
		// Try current dir
		cachePath = filepath.Join(filepath.Dir(exePath), "login_cache.json")
		if err := os.WriteFile(cachePath, data, 0644); err != nil {
			log.Printf("⚠️ Cannot write login_cache.json: %v", err)
			return
		}
	}
	log.Printf("💾 [%s] Updated login_cache.json with new credentials", platformCode)
}

// runCrawl executes the multi-platform crawl and triggers callback
func (s *Server) runCrawl(task *CrawlTask, hasTraceCode bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("💥 PANIC in runCrawl: %v", r)
		}
		s.mu.Lock()
		s.runningPlan = 0
		// Don't clear s.task immediately — keep data for polling
		// s.task will be cleared when a new task is submitted
		s.mu.Unlock()
		log.Printf("🏁 Crawl plan %d completed (duration: %s)", task.PlanID, time.Since(task.StartTime).Round(time.Second))
	}()

	// Auto-renew credentials for pure-API platforms before crawling
	platformList := make([]string, 0, len(task.PlatformMap))
	for code := range task.PlatformMap {
		platformList = append(platformList, code)
	}
	s.autoRenewCredentials(platformList)

	// Launch each platform as a goroutine
	if len(task.PlatformMap) == 0 {
		log.Printf("⚠️ No platforms to crawl, finishing immediately")
		s.mu.Lock()
		s.runningPlan = 0
		s.mu.Unlock()
		return
	}

	for code, cfg := range task.PlatformMap {
		go s.crawlPlatform(task, code, cfg, hasTraceCode)
	}

	// Wait for all platforms to finish
	for range task.AllDone {
		task.DoneCount++
		if task.DoneCount >= task.TotalCount {
			close(task.AllDone)
			break
		}
	}

	// Assemble results and callback to Backend
	s.assembleAndCallback(task)
}

// crawlPlatform runs a single platform crawl
func (s *Server) crawlPlatform(task *CrawlTask, code string, cfg *PlatformConfig, hasTraceCode bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("💥 PANIC in [%s] crawlPlatform: %v", code, r)
			task.Mu.Lock()
			task.Results[code] = &CrawlPlatformResult{Platform: code, Status: 3, Error: fmt.Sprintf("panic: %v", r)}
			task.Mu.Unlock()
			task.AllDone <- struct{}{}
		}
	}()
	log.Printf("🔍 [%s] Starting crawl...", code)
	startTime := time.Now()

	var result *CrawlPlatformResult

	// Convert sku_list to platform.DrugSKU
	skus := make([]platform.DrugSKU, len(task.SkuList))
	for i, item := range task.SkuList {
		drugName := item.Name
		if drugName == "" {
			drugName = item.DrugName // Python Spider uses drug_name
		}
		skus[i] = platform.DrugSKU{
			Name:         drugName,
			Spec:         item.Spec,
			Factory:      item.Factory,
			Barcode:      item.Barcode,
			FactoryAlias: item.FactoryAlias,
		}
	}

	// If platform lacks credentials, try to fetch from login_cache
	if cfg.Token == "" && len(cfg.Cookie) == 0 && len(cfg.CookieDict) == 0 {
		token, cookieDict := s.fetchPlatformCredentials(code)
		if token != "" {
			cfg.Token = token
		}
		if len(cookieDict) > 0 && len(cfg.CookieDict) == 0 {
			cfg.CookieDict = cookieDict
		}
		log.Printf("📋 [%s] Fetched from login_cache: token=%v cookieDict_keys=%d", code, cfg.Token != "", len(cfg.CookieDict))
	}

	// Create platform client based on code
	var client platform.Platform
	switch code {
	case "yjj":
		if cfg.Token != "" {
			client = yjj.NewYJJClient(cfg.Token, "", "")
		} else {
			result = &CrawlPlatformResult{Platform: code, Status: 3, Error: "no token for yjj"}
		}
	case "ysb":
		if cfg.Token != "" {
			client = ysb.NewYSBClient()
			client.SetToken(cfg.Token)
		} else {
			result = &CrawlPlatformResult{Platform: code, Status: 3, Error: "no token for ysb"}
		}
	case "ybm":
		client = ybm.NewYBMClient(cfg.CookieDict)
	case "yyc":
		client = yyc.NewYYCClient(cfg.CookieDict)
	case "yyg":
		// YYG login is pure API (no captcha), always refresh token via auto-login
		username, password := s.getPlatformLoginCredentials("yyg")
		if username == "" || password == "" {
			// Fallback: use cached token if credentials unavailable
			if cfg.Token != "" {
				log.Printf("⚠️ [yyg] No login credentials, trying cached token")
				client = yyg.NewYYGClient(cfg.Token)
			} else {
				result = &CrawlPlatformResult{Platform: code, Status: 3, Error: "no token for yyg and no login credentials"}
			}
		} else {
			log.Printf("🔄 [yyg] Auto-login with username=%s", username)
			yygTemp := yyg.NewYYGClient("")
			loginResult, err := yygTemp.Login(username, password)
			if err != nil {
				log.Printf("❌ [yyg] Auto-login failed: %v, falling back to cached token", err)
				if cfg.Token != "" {
					client = yyg.NewYYGClient(cfg.Token)
				} else {
					result = &CrawlPlatformResult{Platform: code, Status: 3, Error: fmt.Sprintf("auto-login failed: %v", err)}
				}
			} else if t, ok := loginResult["token"]; ok && t != "" {
				log.Printf("✅ [yyg] Auto-login success, new token obtained")
				client = yyg.NewYYGClient(t)
				// Update login_cache with new token/cookie
				s.updateLoginCacheToken("yyg", loginResult)
			} else {
				log.Printf("⚠️ [yyg] Auto-login returned empty token, using cached")
				if cfg.Token != "" {
					client = yyg.NewYYGClient(cfg.Token)
				} else {
					result = &CrawlPlatformResult{Platform: code, Status: 3, Error: "auto-login returned empty token"}
				}
			}
		}
	case "xmyy":
		if cfg.Token != "" {
			client = xmyy.NewXMYYClient(cfg.Token)
		} else {
			result = &CrawlPlatformResult{Platform: code, Status: 3, Error: "no token for xmyy"}
		}
	case "jyjt":
		// JYJT login is pure API (MD5 password + JWT from API), always refresh token via auto-login
		username, password := s.getPlatformLoginCredentials("jyjt")
		if username == "" || password == "" {
			// Fallback: use cached cookie_dict if credentials unavailable
			if len(cfg.CookieDict) > 0 {
				log.Printf("⚠️ [jyjt] No login credentials, trying cached cookie_dict")
				client = jyjt.NewJYJTClient(cfg.CookieDict)
			} else {
				result = &CrawlPlatformResult{Platform: code, Status: 3, Error: "no cookie_dict for jyjt and no login credentials"}
			}
		} else {
			log.Printf("🔄 [jyjt] Auto-login with username=%s", username)
			jyjtTemp := jyjt.NewJYJTClient(nil)
			loginResult, err := jyjtTemp.Login(username, password)
			if err != nil {
				log.Printf("❌ [jyjt] Auto-login failed: %v, falling back to cached", err)
				if len(cfg.CookieDict) > 0 {
					client = jyjt.NewJYJTClient(cfg.CookieDict)
				} else {
					result = &CrawlPlatformResult{Platform: code, Status: 3, Error: fmt.Sprintf("auto-login failed: %v", err)}
				}
			} else if t, ok := loginResult["jzyy__Shopping-Access-Token"]; ok && t != "" {
				log.Printf("✅ [jyjt] Auto-login success, new token obtained")
				// Build cookie_dict from login result
				newCookieDict := make(map[string]interface{})
				for k, v := range loginResult {
					newCookieDict[k] = v
				}
				client = jyjt.NewJYJTClient(newCookieDict)
				s.updateLoginCacheToken("jyjt", loginResult)
			} else {
				log.Printf("⚠️ [jyjt] Auto-login returned empty token, using cached")
				if len(cfg.CookieDict) > 0 {
					client = jyjt.NewJYJTClient(cfg.CookieDict)
				} else {
					result = &CrawlPlatformResult{Platform: code, Status: 3, Error: "auto-login returned empty token"}
				}
			}
		}
	case "dek":
		client = dek.NewDEKClient(cfg.CookieDict)
	case "ykx":
		client = ykx.NewYKXClient(cfg.CookieDict)
	default:
		result = &CrawlPlatformResult{Platform: code, Status: 3, Error: fmt.Sprintf("platform %s not implemented yet", code)}
	}

	if result == nil && client != nil {
		var allItems []platform.DrugResult
		for _, sku := range skus {
			// Small delay between requests to avoid rate limiting
			if rand.Float64() < 0.5 {
				time.Sleep(time.Duration(300+rand.Float64()*700) * time.Millisecond)
			}

			searchResult, err := client.SearchDrugs(sku, hasTraceCode)
			if err != nil {
				log.Printf("❌ [%s] Search error for %s: %v", code, sku.Name, err)
				continue
			}

			if searchResult.Status == 2 {
				allItems = append(allItems, searchResult.Items...)
				log.Printf("✅ [%s] %s: %d items found", code, sku.Name, len(searchResult.Items))
			} else {
				log.Printf("⚠️ [%s] %s: %s (status=%d)", code, sku.Name, searchResult.Error, searchResult.Status)
			}
		}

		result = &CrawlPlatformResult{
			Platform: code,
			Status:   2,
			Count:    len(allItems),
			Items:    allItems,
			StartTime: startTime,
			EndTime:  time.Now(),
		}
	}

	if result != nil {
		result.StartTime = startTime
		result.EndTime = time.Now()
	}

	task.Mu.Lock()
	task.Results[code] = result
	task.Mu.Unlock()

	log.Printf("🏁 [%s] Crawl done: status=%d count=%d duration=%s",
		code, result.Status, result.Count, result.EndTime.Sub(result.StartTime).Round(time.Millisecond))

	// Signal completion
	task.AllDone <- struct{}{}
}

// assembleAndCallback assembles crawl results and POSTs to Backend
func (s *Server) assembleCrawlResults(task *CrawlTask) map[string]interface{} {
	// Build merged data in Python Spider compatible format
	skus := make([]map[string]interface{}, len(task.SkuList))
	for i, item := range task.SkuList {
		skus[i] = map[string]interface{}{
			"barcode": item.Barcode,
			"name":    item.Name,
			"number":  item.Number,
			"spec":    item.Spec,
			"factory": item.Factory,
		}
	}

	// Collect all drug results grouped by SKU
	target := make([]map[string]interface{}, 0)
	goodsStats := make(map[string]interface{})

	for _, sku := range task.SkuList {
		skuDrugs := make([]map[string]interface{}, 0)
		for _, result := range task.Results {
			if result.Status != 2 {
				continue
			}
			for _, item := range result.Items {
				if item.Barcode == sku.Barcode {
					drugMap := map[string]interface{}{
						"品名":    sku.Barcode,
						"平台":    item.Platform,
						"药品名":   item.DrugName,
						"厂家":    item.Factory,
						"规格":    item.Spec,
						"价格":    item.Price,
						"店铺ID":   fmt.Sprintf("%s_%d", item.Platform, item.StoreID),
						"店铺名":   item.StoreName,
						"销量":    item.SalesNum,
						"效期":    item.ValidDate,
						"图片URL":  item.ImageURL,
						"追溯码":   item.HasTraceCode,
						"最低购买量": item.MinBuyNum,
						"单位":    item.Unit,
						"药品ID":  item.DrugID,
					"库存":    item.Inventory,
					"可售数":   item.CanSaleNum,
					"效期":    item.ValidDate,
					}
					if len(item.Tags) > 0 {
						drugMap["标签"] = item.Tags
					}
					skuDrugs = append(skuDrugs, drugMap)
				}
			}
		}
		if len(skuDrugs) > 0 {
			target = append(target, skuDrugs...)
		}

		// Per-SKU stats
		platformSet := make(map[string]bool)
		storeSet := make(map[string]bool)
		totalCount := 0
		for _, result := range task.Results {
			if result.Status != 2 {
				continue
			}
			for _, item := range result.Items {
				if item.Barcode == sku.Barcode {
					platformSet[item.Platform] = true
					storeSet[fmt.Sprintf("%d", item.StoreID)] = true
					totalCount++
				}
			}
		}
		if totalCount > 0 {
			goodsStats[sku.Barcode] = map[string]interface{}{
				"count_platform": len(platformSet),
				"count_shop":     len(storeSet),
				"count":          totalCount,
			}
		}
	}

	// Spider status JSON (per-SKU per-platform status)
	spiderStatusJSON := make([]map[string]interface{}, 0)
	for _, sku := range task.SkuList {
		skuStatus := map[string]interface{}{
			"name":    sku.Name,
			"barcode": sku.Barcode,
			"factory": sku.Factory,
			"spec":    sku.Spec,
			"count":   0,
		}
		for _, result := range task.Results {
			skuCount := 0
			if result.Status == 2 {
				for _, item := range result.Items {
					if item.Barcode == sku.Barcode {
						skuCount++
					}
				}
			}
			skuStatus[fmt.Sprintf("%s_status", result.Platform)] = result.Status
			skuStatus[fmt.Sprintf("%s_count", result.Platform)] = skuCount
			skuStatus[fmt.Sprintf("%s_endtime", result.Platform)] = result.EndTime.Format("2006-01-02 15:04:05")
			skuStatus["count"] = skuStatus["count"].(int) + skuCount
		}
		spiderStatusJSON = append(spiderStatusJSON, skuStatus)
	}

	mergedData := map[string]interface{}{
		"target":              target,
		"shop_data":           []interface{}{},
		"platform_data":       []interface{}{},
		"sku_list":            skus,
		"version":             spiderVersion,
		"goods_stats":         goodsStats,
		"spider_status_json":  spiderStatusJSON,
	}

	return mergedData
}

// assembleAndCallback assembles results, writes to file, and calls Backend
func (s *Server) assembleAndCallback(task *CrawlTask) {
	log.Printf("📊 Assembling results for plan %d...", task.PlanID)

	mergedData := s.assembleCrawlResults(task)

	// Write result to log file (compatible with Python Spider)
	resultDir := filepath.Join(filepath.Dir(os.Args[0]), "..", "logs", "result_out")
	os.MkdirAll(resultDir, 0755)
	resultFile := filepath.Join(resultDir, fmt.Sprintf("%d_result_out.log", task.PlanID))
	if data, err := json.MarshalIndent(mergedData, "", "  "); err == nil {
		os.WriteFile(resultFile, data, 0644)
		log.Printf("📁 Result written to %s", resultFile)
	}

	// Build spider_status_json for form_data
	spiderStatusJSON := mergedData["spider_status_json"]

	// Check if all platforms are done
	allDone := true
	for _, result := range task.Results {
		if result.Status != 2 {
			allDone = false
			break
		}
	}

	_ = allDone

	// Upload result file and callback to Backend
	formData := map[string]interface{}{
		"order_plan_id":         task.PlanID,
		"spider_status_json":    spiderStatusJSON,
		"spider_result_file_url": nil,
		"goods_stats":           mergedData["goods_stats"],
	}

	// Upload file to COS via Backend
	if _, err := os.Stat(resultFile); err == nil {
		uploadURL, key, err := s.uploadResultFile(resultFile, task.PlanID, task.Token)
		if err != nil {
			log.Printf("⚠️ File upload failed: %v", err)
		} else if key != "" {
			formData["spider_result_file_url"] = key
			_ = uploadURL
		}
	}

	// POST callback to Backend
	callbackURL := s.serverDomain + "/api/v1/order_inventory/best_price/"
	for retry := 1; retry <= 5; retry++ {
		log.Printf("📤 Callback to Backend (attempt %d): %s", retry, callbackURL)

		body, _ := json.Marshal(formData)
		req, _ := http.NewRequest("POST", callbackURL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if task.Token != "" {
			req.Header.Set("Token", task.Token)
		}

		client := &http.Client{Timeout: 60 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("⚠️ Callback attempt %d error: %v", retry, err)
			time.Sleep(time.Duration(retry*3) * time.Second)
			continue
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("📤 Callback response: %d %s", resp.StatusCode, string(respBody[:min(len(respBody), 200)]))

		if resp.StatusCode == 200 {
			var respJSON map[string]interface{}
			if err := json.Unmarshal(respBody, &respJSON); err == nil {
				if respJSON["code"] == float64(100) || respJSON["code"] == "100" {
					log.Printf("✅ Callback to Backend succeeded!")
					return
				}
			}
		}

		time.Sleep(time.Duration(retry*3) * time.Second)
	}

	log.Printf("❌ All callback attempts failed for plan %d", task.PlanID)
}

// uploadResultFile uploads result file to AI server local file server (no COS)
func (s *Server) uploadResultFile(filePath string, planID int, token string) (resultURL, key string, err error) {
	// Read the result file
	f, err := os.Open(filePath)
	if err != nil {
		return "", "", fmt.Errorf("open result file failed: %w", err)
	}
	defer f.Close()
	fi, _ := f.Stat()

	// Upload to AI server local file server via HTTP PUT
	uploadURL := s.fileServerURL + "/" + fmt.Sprintf("%d_result_out.json", planID)
	req, _ := http.NewRequest("PUT", uploadURL, f)
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = fi.Size()

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("upload to file server failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 201 && resp.StatusCode != 204 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("upload returned %d: %s", resp.StatusCode, string(respBody[:min(len(respBody), 200)]))
	}

	resultURL = uploadURL
	key = uploadURL // Use the full URL as the key (Backend will download from this URL)
	log.Printf("📁 Uploaded result to local file server: %s (%d bytes)", uploadURL, fi.Size())
	return resultURL, key, nil
}

func (s *Server) handleSelfUpdate(w http.ResponseWriter, req ApplyAllSpiderRequest) {
	jsonResp(w, http.StatusOK, map[string]interface{}{
		"code": 100,
		"msg":  "Go Spider does not support self-update via plan_id=-1",
	})
}

func (s *Server) handleGetPlanSchedule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResp(w, http.StatusBadRequest, map[string]interface{}{"code": 400, "msg": err.Error()})
		return
	}

	planID, _ := req["plan_id"].(float64)

	s.mu.Lock()
	task := s.task
	s.mu.Unlock()

	if task == nil || int(planID) != task.PlanID {
		jsonResp(w, http.StatusOK, map[string]interface{}{
			"code": 200,
			"data":        []interface{}{},
			"all_status":  true,
		})
		return
	}

	// Build drug_dict (Python Spider compatible format)
	drugDict := make(map[string]map[string]interface{})
	for _, sku := range task.SkuList {
		entry := map[string]interface{}{
			"name":    sku.Name,
			"barcode": sku.Barcode,
			"factory": sku.Factory,
			"spec":    sku.Spec,
			"count":   0,
		}
		for code, result := range task.Results {
			skuCount := 0
			if result.Status == 2 {
				for _, item := range result.Items {
					// Match by barcode OR by drug name + factory
					if item.Barcode == sku.Barcode ||
						(item.DrugName == sku.Name && item.Factory == sku.Factory) ||
						(item.DrugName == sku.Name && sku.Factory == "") {
						skuCount++
					}
				}
			}
			entry[code+"_status"] = result.Status
			entry[code+"_count"] = skuCount
			entry[code+"_endtime"] = result.EndTime.Format("2006-01-02 15:04:05")
			entry["count"] = entry["count"].(int) + skuCount
		}
		drugDict[sku.Barcode] = entry
	}

	// Check if all done
	allDone := true
	for _, result := range task.Results {
		if result.Status != 2 {
			allDone = false
			break
		}
	}

	// Convert to sorted list
	values := make([]map[string]interface{}, 0, len(drugDict))
	for _, v := range drugDict {
		values = append(values, v)
	}

	jsonResp(w, http.StatusOK, map[string]interface{}{
		"code":       100,
		"data":       values,
		"all_status": allDone,
	})
}

func (s *Server) handlePlanStatus(w http.ResponseWriter, r *http.Request) {
	planIDStr := r.URL.Query().Get("plan_id")
	if planIDStr == "" {
		jsonResp(w, http.StatusOK, map[string]interface{}{
			"code":   200,
			"plan_id": nil,
			"msg":    "任务执行完成",
		})
		return
	}

	s.mu.Lock()
	task := s.task
	s.mu.Unlock()

	if task == nil {
		jsonResp(w, http.StatusOK, map[string]interface{}{
			"code":   200,
			"plan_id": planIDStr,
			"msg":    "任务执行完成",
		})
		return
	}

	// Check if all platforms done
	allDone := true
	for _, result := range task.Results {
		if result.Status != 2 {
			allDone = false
			break
		}
	}

	code := 200
	msg := "任务执行完成"
	if !allDone && s.GetRunningPlanID() != 0 {
		code = 201
		msg = "任务进行中"
	}

	jsonResp(w, http.StatusOK, map[string]interface{}{
		"code":   code,
		"plan_id": planIDStr,
		"msg":    msg,
	})
}


// handlePlatformLogin handles POST /platform/login
func (s *Server) handlePlatformLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResp(w, http.StatusBadRequest, map[string]interface{}{"code": 400, "msg": "POST only"})
		return
	}
	var req struct {
		Platform string `json:"platform"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResp(w, http.StatusBadRequest, map[string]interface{}{"code": 400, "msg": "invalid request: " + err.Error()})
		return
	}
	platformCode := strings.ToLower(req.Platform)
	log.Printf("[login] Platform login request: %s user=%s", platformCode, req.Username)

	var cookieDict map[string]string
	var err error

	switch platformCode {
	case "yyg":
		client := yyg.NewYYGClient("")
		cookieDict, err = client.Login(req.Username, req.Password)
	case "dek":
		client := dek.NewDEKClient(nil)
		cookieDict, err = client.Login(req.Username, req.Password)
	case "jyjt":
		client := jyjt.NewJYJTClient(nil)
		cookieDict, err = client.Login(req.Username, req.Password)
	case "xmyy":
		client := xmyy.NewXMYYClient("")
		cookieDict, err = client.Login(req.Username, req.Password)
	case "yjj":
		client := yjj.NewYJJClient("", "", "")
		cookieDict, err = client.Login(req.Username, req.Password)
	case "ysb":
		client := ysb.NewYSBClient()
		cookieDict, err = client.Login(req.Username, req.Password)
	case "yyc":
		client := yyc.NewYYCClient(nil)
		cookieDict, err = client.Login(req.Username, req.Password)
	default:
		jsonResp(w, http.StatusOK, map[string]interface{}{
			"code": 404,
			"msg":  fmt.Sprintf("Platform '%s' does not support API login. Please use browser login.", platformCode),
		})
		return
	}

	if err != nil {
		log.Printf("[login] %s login failed: %v", platformCode, err)
		jsonResp(w, http.StatusOK, map[string]interface{}{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	// Save to login cache
	cacheMap := make(map[string]interface{})
	for k, v := range cookieDict {
		cacheMap[k] = v
	}
	lc := cache.GetCache("login_cache.json")
	lc.Set(platformCode, cacheMap, "", req.Username)
	// Also update in-memory cache
	s.mu.Lock()
	if s.loginCache == nil {
		s.loginCache = make(map[string]map[string]interface{})
	}
	s.loginCache[platformCode] = cacheMap
	s.mu.Unlock()

	log.Printf("[login] %s login success, cached credentials", platformCode)
	jsonResp(w, http.StatusOK, map[string]interface{}{
		"code": 100,
		"msg":  "login success",
		"data": map[string]interface{}{
			"platform":  platformCode,
			"username":  req.Username,
			"has_token": cookieDict != nil && len(cookieDict) > 0,
		},
	})
}

// ============= Helpers =============

func jsonResp(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func compressFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	gzWriter := gzip.NewWriter(dstFile)
	defer gzWriter.Close()

	_, err = io.Copy(gzWriter, srcFile)
	return err
}

// suppress unused import warnings
var (
	_ = strings.TrimSpace
	_ = os.MkdirAll
	_ = rand.Intn
	_ = bytes.NewReader
)



// autoLoginPureAPIPlatforms automatically logs in all platforms that support pure API login
func (s *Server) autoLoginPureAPIPlatforms() {
	time.Sleep(3 * time.Second) // Wait for server to be ready

	// Pure API login platforms: yyg, jyjt, dek
	pureAPIPlatforms := []string{"yyg", "jyjt", "dek"}

	for _, code := range pureAPIPlatforms {
		// Check if already logged in and not expired
		s.mu.Lock()
		if existing, ok := s.loginCache[code]; ok {
			if expired, _ := existing["expired"].(bool); !expired {
				s.mu.Unlock()
				log.Printf("📋 [auto-login] %s already logged in, skipping", code)
				continue
			}
		}
		s.mu.Unlock()

		// Get credentials
		username, password := s.getPlatformLoginCredentials(code)
		if username == "" || password == "" {
			log.Printf("⚠️ [auto-login] %s: no credentials available, skipping", code)
			continue
		}

		log.Printf("🔄 [auto-login] %s: logging in with username=%s", code, username)

		var cookieDict map[string]string
		var err error

		switch code {
		case "yyg":
			client := yyg.NewYYGClient("")
			cookieDict, err = client.Login(username, password)
		case "jyjt":
			client := jyjt.NewJYJTClient(nil)
			cookieDict, err = client.Login(username, password)
		case "dek":
			client := dek.NewDEKClient(nil)
			cookieDict, err = client.Login(username, password)
		}

		if err != nil {
			log.Printf("❌ [auto-login] %s: login failed: %v", code, err)
			continue
		}

		// Save to login cache
		s.updateLoginCacheToken(code, cookieDict)
		log.Printf("✅ [auto-login] %s: login success", code)
	}

	log.Printf("🏁 [auto-login] Auto-login complete for pure-API platforms")
}
