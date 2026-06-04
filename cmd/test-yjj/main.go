package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"yaocai-spider-go/internal/yjj"
)

func main() {
	token := flag.String("token", "", "YJJ zhcaiToken")
	userBasicID := flag.String("uid", "", "YJJ userBasicId")
	companyID := flag.String("cid", "", "YJJ companyId")
	keyword := flag.String("keyword", "阿莫西林胶囊", "Search keyword")
	factory := flag.String("factory", "华北制药", "Factory name")
	spec := flag.String("spec", "0.5g*24粒/盒", "Drug spec")
	flag.Parse()

	fmt.Println("=== Go Spider v3.0.0 YJJ Test ===")

	if *token == "" {
		fmt.Println("⚠️ Usage: test-yjj -token <zhcaiToken> [-uid <userBasicId>] [-cid <companyId>]")
		os.Exit(1)
	}

	client := yjj.NewYJJClient(*token, *userBasicID, *companyID)

	fmt.Printf("\n--- Testing QuerySearchFilters: %s / %s / %s ---\n", *keyword, *factory, *spec)
	result, err := client.QuerySearchFilters(*keyword, *factory, *spec)
	if err != nil {
		log.Fatalf("QuerySearchFilters failed: %v", err)
	}
	fmt.Printf("✅ Aggregation result: code=%d factories=%v specs_count=%d\n",
		result.Code, result.MatchedFactories, len(result.MatchedSpecs))

	if len(result.MatchedFactories) > 0 {
		fmt.Printf("\n--- Testing SearchDrugItems ---\n")
		searchResult, err := client.SearchDrugItems(*keyword, result.MatchedFactories, result.MatchedSpecs, false)
		if err != nil {
			log.Fatalf("SearchDrugItems failed: %v", err)
		}
		fmt.Printf("✅ Search result: total=%d items\n", searchResult.Total)
		for i, item := range searchResult.Items {
			if i >= 10 {
				fmt.Printf("   ... and %d more\n", searchResult.Total-10)
				break
			}
			fmt.Printf("   %s | %s | ¥%.2f | %s | barNo=%s\n",
				item.ItemStoreName, item.ManufactureName, item.ItemPrice, item.StoreName, item.BarNo)
		}
	}
}
