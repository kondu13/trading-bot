package common

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func DoesListContainsString(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func WriteMapToJSONFile(data any, filename string) error {
	// Marshal the map to JSON
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	// Write to file
	err = os.WriteFile(filename, jsonData, 0644)
	if err != nil {
		return err
	}

	return nil
}

func ReadJsonFile(filePath string) *json.Decoder {
	jsonFile, err := os.Open(filePath)
	if err != nil {
		Log(ERROR, "reading the file: %s.\n%s", filePath, err)
	}
	decoder := json.NewDecoder(jsonFile)
	return decoder
}

func PermutationOverListElements(allElements []string) [][]string {
	var result [][]string
	for i := 0; i < len(allElements); i++ {
		for j := i + 1; j < len(allElements); j++ {
			result = append(result, []string{allElements[i], allElements[j]})
			result = append(result, []string{allElements[j], allElements[i]})
		}
	}
	return result
}

func CalculateTradeProfit(buyOffer *CoinMeta, sellOffer *CoinMeta) float64 {
	return (sellOffer.Price - buyOffer.Price) / buyOffer.Price
}

func IsValidOfferByOrderAmount(offer *CoinMeta) bool {
	if IsUSDTBaseExchange(offer.Exchange) {
		return offer.Price*offer.Quantity >= MinValidOrderAmount
	}
	return offer.Price*offer.Quantity >= MinValidOrderAmountInrBase
}

func IsUSDTBaseExchange(exchange string) bool {
	for _, ex := range USDTBaseExchanges {
		if ex == exchange {
			return true
		}
	}
	return false
}

func CreateExchangeSymbolForCSX(coin, exchange string) string {
	symbol := fmt.Sprintf("%s/%s", coin, BaseINR)
	if IsUSDTBaseExchange(exchange) {
		symbol = fmt.Sprintf("%s/%s", coin, BaseUSDT)
	}
	return symbol
}

func ExtractCoinNameFromSymbol(symbol string) (string, string) {
	sym := strings.Split(symbol, "/")
	coin := sym[0]
	baseCurrency := sym[1]
	return coin, baseCurrency
}

func AreSlicesEqual(slice1, slice2 []string) bool {
	if len(slice1) != len(slice2) {
		return false
	}
	// All element of slice1 is in slice2
	for _, v := range slice1 {
		if !DoesListContainsString(slice2, v) {
			return false
		}
	}
	// All element of slice2 is in slice1
	for _, v := range slice2 {
		if !DoesListContainsString(slice1, v) {
			return false
		}
	}
	return true
}
