/*
1. Fetch latest coin precisions on all exchanges.
2. List down all common coins across all exchanges.
*/
package utils

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"trade_go/internal/api"
	"trade_go/internal/common"
	"trade_go/internal/configs"
)

type allCoinExchangePrecisionsType map[string]map[string]map[string]float64

func readCoinsFromExchangePrecisions(latestCoinPrecisions allCoinExchangePrecisionsType) map[string][]string {
	exchangeWiseCoins := make(map[string][]string)
	for exchange, exchangeCoins := range latestCoinPrecisions {
		coins := make([]string, 0, len(exchangeCoins))
		for coin := range exchangeCoins {
			if common.DoesListContainsString(configs.NotTradedCoins, coin) {
				continue
			}
			coins = append(coins, coin)
		}
		exchangeWiseCoins[exchange] = coins
	}
	return exchangeWiseCoins
}

func fetchAllCoinPrecisions(apiClient *api.ApiTradingClient, writeToJson bool) allCoinExchangePrecisionsType {
	// Reads all coin precision from AllExchanges and saves to JSON file.
	allExchangePrecisions := make(allCoinExchangePrecisionsType)

	for _, exchange := range common.AllExchanges {

		var params api.GenericMap = make(api.GenericMap)
		params["exchange"] = exchange

		validCoins, err := apiClient.CheckCoins(params)

		if err != nil {
			common.Log(common.INFO, "ERROR in fetching valid coins for %s.\n%v", exchange, err)
			continue
		}
		sort.Strings(validCoins)

		coinPrecisionsResponse, err := apiClient.GetExchangePrecision(params)

		if err != nil {
			common.Log(common.INFO, "ERROR in fetching coin precisions for %s.\n%v\n", exchange, err)
			continue
		}

		allCoinPrecisions, ok := coinPrecisionsResponse["data"].(map[string]interface{})
		if !ok {
			common.Log(common.ERROR, "fetching all coin precision for %s\n", exchange)
		}
		allCoinExchangePrecisions, ok := allCoinPrecisions[exchange].(map[string]interface{})
		if !ok {
			common.Log(common.ERROR, "asserting exchange coin precisions map for %s\n", exchange)
			continue
		}

		validCoinPrecisions := make(map[string]map[string]float64)

		for _, symbol := range validCoins {
			coinPrecision, ok := allCoinExchangePrecisions[symbol].(map[string]interface{})
			if !ok {
				common.Log(common.ERROR, "Missing %s precision for %s\n", symbol, exchange)
				continue
			}
			coinPrecisionMap := make(map[string]float64)
			for precisionType, precision := range coinPrecision {
				precisionVal, _ := precision.(float64)
				coinPrecisionMap[precisionType] = precisionVal
			}
			coin, _ := common.ExtractCoinNameFromSymbol(symbol)
			validCoinPrecisions[coin] = coinPrecisionMap
		}
		if len(validCoinPrecisions) >= 1 {
			allExchangePrecisions[exchange] = validCoinPrecisions
			common.Log(common.INFO, "Found %d coins on %s exchange\n", len(validCoinPrecisions), exchange)
		}
	}

	if writeToJson {
		common.WriteMapToJSONFile(allExchangePrecisions, common.ExchangePrecisionsPath)
	}

	return allExchangePrecisions

}

func fetchCommonCoinsAcrossExchanges(apiClient *api.ApiTradingClient, writeToJson bool) []string {
	var commonCoins []string
	param := map[string]interface{}{
		"exchange": "",
	}

	coinCount := make(map[string]int)
	for _, exchange := range common.AllExchanges {
		param["exchange"] = exchange
		validSymbols, _ := apiClient.CheckCoins(param)

		if exchange == common.ExcCoinswitch {
			var allCoins []string
			for _, symbol := range validSymbols {
				coin, _ := common.ExtractCoinNameFromSymbol(symbol)
				allCoins = append(allCoins, coin)
			}
			// Select coins by 24 hour trade volume.
			past24HourTopVolumeCoins := selectCoinsByTradeVolume(
				apiClient, allCoins, exchange,
				common.ArbitrageCoinTradeVolumeHour,
				common.IntervalHour,
				common.ArbitrageCoinMinVolumePercentile,
			)

			// Select coins by past 15 min trade volume.
			past15MinTopVolumeCoins := selectCoinsByTradeVolume(
				apiClient, allCoins, exchange,
				common.ArbitrageCoinTradeVolumeMin,
				common.IntervalMinute,
				common.ArbitrageCoinMinVolumePercentile,
			)

			allSelectedCoins := append(past24HourTopVolumeCoins, past15MinTopVolumeCoins...)

			validSymbols = []string{}
			coinMap := make(map[string]int)
			for _, coin := range allSelectedCoins {
				_, exists := coinMap[coin]
				if exists {
					continue
				}
				coinMap[coin] = 1
				validSymbols = append(validSymbols, fmt.Sprintf("%s/%s", coin, common.BaseINR))
			}
			common.Log(common.INFO, "%d coins selected from %s based on the trade volume", len(validSymbols), common.ExcCoinswitch)
		}

		for _, symbol := range validSymbols {
			coin, _ := common.ExtractCoinNameFromSymbol(symbol)

			_, exists := coinCount[coin]
			if exists {
				coinCount[coin] += 1
			} else {
				coinCount[coin] = 1
			}
		}
	}
	for coin, count := range coinCount {
		if count > 1 && coin != "USDT" && !common.DoesListContainsString(configs.NotTradedCoins, coin) {
			commonCoins = append(commonCoins, coin)
		}
	}
	sort.Strings(commonCoins)
	common.Log(common.INFO, "Found %d common coins across exchanges: %v", len(commonCoins), common.AllExchanges)

	if writeToJson {
		common.WriteMapToJSONFile(commonCoins, common.CommonArbitrageCoinsPath)
	}

	return commonCoins
}

func calculatePercentile(data []float64, percentile float64) float64 {
	index := (percentile / 100) * float64(len(data))
	return data[int(index)]
}

func selectCoinsByTradeVolume(
	apiClient *api.ApiTradingClient,
	allCoins []string,
	exchange string,
	numPastDuration int,
	intervalType string,
	minPercentile float64,
) []string {
	/*
		1. Find the past 24 hour volume, sort in descending order and select top 80 percentil coins.
		2. Find the past 15 min volume, sort in descending order and select top 80 percentil coins.
		3. Use the union of the 2 set as the selecting valid coins for trading.
	*/
	var wg sync.WaitGroup

	coinVolumesChan := make(chan common.CoinTradeVolume)

	batchCounter := 0
	for _, coin := range allCoins {
		if batchCounter == 20 {
			time.Sleep(3 * time.Second)
			batchCounter = 0
		}
		wg.Add(1)
		go calculateINRTradeVolume(&wg, coinVolumesChan, apiClient, coin, exchange, numPastDuration, intervalType)
		batchCounter += 1
	}

	go func() {
		wg.Wait()
		close(coinVolumesChan)
	}()

	var volumes []float64
	coinMap := make(map[string]common.CoinTradeVolume)
	for result := range coinVolumesChan {
		volumes = append(volumes, result.VolumeInINR)
		coinMap[result.Coin] = result
	}
	if len(volumes) == 0 {
		common.Log(common.WARNING, "found no valid volumes for %d coins for last %d %s", len(allCoins), numPastDuration, intervalType)
		return []string{}
	}
	sort.Float64s(volumes)

	percentileValue := calculatePercentile(volumes, minPercentile)

	var selectedCoins []string
	for _, coin := range allCoins {
		if volume, exists := coinMap[coin]; exists {
			if volume.VolumeInINR >= percentileValue {
				selectedCoins = append(selectedCoins, coin)
			}
		}
		// else {
		// 	selectedCoins = append(selectedCoins, coin)
		// }
	}
	common.Log(common.INFO, "selected %d/%d coins with valid tradeVolume >= %.3f : pastDuration: %d %s: bottomPercentile: %.1f on %s", len(selectedCoins), len(allCoins), percentileValue, numPastDuration, intervalType, minPercentile, exchange)
	return selectedCoins
}

func calculateINRTradeVolume(
	wg *sync.WaitGroup,
	volumeChan chan common.CoinTradeVolume,
	apiClient *api.ApiTradingClient,
	coin, exchange string, numPastDuration int, intervalType string,
) {
	defer wg.Done()

	tradeVolume := common.CoinTradeVolume{
		Coin:        coin,
		Exchange:    exchange,
		VolumeInINR: 0,
	}
	istLocation := time.FixedZone("IST", 5*60*60+30*60) // UTC +5:30

	endTime := time.Now().UTC()

	duration := time.Duration(-numPastDuration)
	startTime := endTime.Add(duration * time.Hour)
	if intervalType == common.IntervalMinute {
		startTime = endTime.Add(duration * time.Minute)
	}
	startTime = startTime.In(istLocation)
	endTime = endTime.In(istLocation)

	// common.Log(common.INFO, "Start: %v -> End: %v", startTime, endTime)

	// Convert the time to milliseconds since Unix epoch
	endTimeMillis := endTime.UnixMilli()
	startTimeMillis := startTime.UnixMilli()
	base := common.BaseINR
	if common.IsUSDTBaseExchange(exchange) {
		base = common.BaseUSDT
	}

	interval := "60"
	if intervalType == common.IntervalMinute {
		interval = "5"
	}
	// Create the params map with the calculated values
	params := map[string]interface{}{
		"end_time":   endTimeMillis,
		"start_time": startTimeMillis,
		"symbol":     fmt.Sprintf("%s/%s", coin, base),
		"interval":   interval,
		"exchange":   exchange,
	}

	// Fetch candlestick data using your API client
	candleStickData, err := apiClient.GetCandlestickData(params)
	if err != nil {
		// common.Log(common.ERROR, "fetching candlestick data for %v: `%v`", params, err)
		return
	}

	// Calculate the total volume.
	var totalVolumeInInr float64
	for _, entry := range candleStickData {
		totalVolumeInInr += entry.Volume * entry.Close
	}
	if common.IsUSDTBaseExchange(exchange) {
		totalVolumeInInr *= 90
	}
	tradeVolume.VolumeInINR = totalVolumeInInr
	volumeChan <- tradeVolume
}

func isCommissionInAccountSufficient(apiClient *api.ApiTradingClient) bool {
	financialInfo, err := apiClient.GetFinancialInfo()
	if err != nil {
		common.Log(common.ERROR, "fetching financial data: `%v`", err)
		return true
	}
	common.ReadWriteMutex.RLock()
	allCoinBalance := common.AccountPortfolioBalance
	common.ReadWriteMutex.RUnlock()

	var portfolioBalance float64
	for coin, balanceMetadata := range allCoinBalance {
		if common.DoesListContainsString(configs.NotTradedCoins, coin) {
			continue
		}
		coinBalance := balanceMetadata.Amount
		if coinBalance >= common.MinValidOrderAmountInrBase {
			portfolioBalance += balanceMetadata.Amount
		}
	}

	commission := configs.CommissionRate * financialInfo.PnL
	common.Log(common.INFO, "TDS: %.3f -> CurrentValue: %.3f -> PnL: %.3f -> portfolio: %.3f -> commission: %.3f", financialInfo.TotalTDS, financialInfo.CurrentValue, financialInfo.PnL, portfolioBalance, commission)
	if financialInfo.PnL > 0 && portfolioBalance <= commission {
		common.Log(common.WARNING, "STOP BOT since portfolio balance: %.3f -> Commission: %.3f", portfolioBalance, commission)
		return false
	}
	return true
}
