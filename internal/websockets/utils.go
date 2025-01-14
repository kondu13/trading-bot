package websockets

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"trade_bot/internal/common"

	"github.com/redis/go-redis/v9"
)

func convertCurrencyPair(pair string) string {
	/*
		From csx websocket, the symbol is of the format: "ETH,INR"
		From binance websocket, the symbol is of the format: "ETHINR"
		From kucoin websocket, the symbol is of the format: "ETH-USDT"
	*/
	result := strings.ReplaceAll(pair, "-", "")
	result = strings.ReplaceAll(result, ",", "")
	return result
}

func ReadOrderBookFromRedis(redisClient *redis.Client, coin string, exchange string) (DepthUpdate, error) {
	/* Read the order book data from Redis */
	base := common.BaseINR
	if common.DoesListContainsString(common.USDTBaseExchanges, exchange) {
		base = common.BaseUSDT
	}
	coin = strings.ToUpper(coin)
	symbol := fmt.Sprintf("%s%s", coin, base)
	exchangeCurrencyKey := fmt.Sprintf(redisDepthKeyVar, exchange, symbol)

	coinDepth := DepthUpdate{
		Symbol:   symbol,
		Exchange: exchange,
	}

	ctx := context.Background()
	redisData, err := redisClient.HGetAll(ctx, exchangeCurrencyKey).Result()
	if err != nil {
		return coinDepth, fmt.Errorf("error fetching data from Redis: %v", err)
	}

	askJson, ok := redisData["asks"]
	if !ok {
		return coinDepth, fmt.Errorf("%s -> asks not found in redis", exchangeCurrencyKey)
	}
	var asks [][]string
	if err := json.Unmarshal([]byte(askJson), &asks); err != nil {
		return coinDepth, fmt.Errorf("%s -> could not unmarshal ask: %v", exchangeCurrencyKey, err)
	}

	bidJson, ok := redisData["bids"]
	if !ok {
		return coinDepth, fmt.Errorf("%s -> bids not found in redis", exchangeCurrencyKey)
	}
	var bids [][]string
	if err := json.Unmarshal([]byte(bidJson), &bids); err != nil {
		return coinDepth, fmt.Errorf("%s -> could not unmarshal bid: %v", exchangeCurrencyKey, err)
	}

	timestampStr, ok := redisData["timestamp"]
	if !ok {
		return coinDepth, fmt.Errorf("%s -> timestamp not found in redis", exchangeCurrencyKey)
	}
	timestamp, err := time.Parse(time.RFC3339, timestampStr)
	if err != nil {
		return coinDepth, fmt.Errorf("%s could not convert to valid timestamp: %v", exchangeCurrencyKey, err)
	}

	coinDepth.Asks = asks
	coinDepth.Bids = bids
	coinDepth.Timestamp = timestamp
	return coinDepth, nil
}

func saveOrderBookToRedis(redisClient *redis.Client, orderBook DepthUpdate) error {
	/*
		Save the order book data in Redis.
		Key: binance:BTCUSDT
			Field: bids      Value: [JSON encoded bid data] []
			Field: asks      Value: [JSON encoded ask data]
			Field: timestamp Value: [Latest timestamp]
	*/
	currencyPair := orderBook.Symbol
	timestamp := orderBook.Timestamp
	exchange := orderBook.Exchange

	exchangeCurrencyKey := fmt.Sprintf(redisDepthKeyVar, exchange, currencyPair)

	bids := orderBook.Bids[:min(len(orderBook.Bids), bidAskDepth)]
	bidDataJSON, err := json.Marshal(bids)
	if err != nil {
		return fmt.Errorf("%s -> could not convert bid data to JSON: %v", exchangeCurrencyKey, err)
	}

	asks := orderBook.Asks[:min(len(orderBook.Asks), bidAskDepth)]
	askDataJSON, err := json.Marshal(asks)
	if err != nil {
		return fmt.Errorf("%s -> could not convert ask data to JSON: %v", exchangeCurrencyKey, err)
	}

	ctx := context.Background()
	err = redisClient.HSet(ctx, exchangeCurrencyKey,
		"asks", askDataJSON,
		"bids", bidDataJSON,
		"timestamp", timestamp,
	).Err()
	if err != nil {
		return fmt.Errorf("%s -> error storing bidask data in Redis: %v", exchangeCurrencyKey, err)
	}
	return nil
}

func isRawDepthValid(depthData [][]string) bool {
	// Validate raw bid ask is valid or not
	if len(depthData) < common.MaxDepth {
		return false
	}
	isValidDepth := false
	for depth, rawDepth := range depthData {
		if depth >= common.MaxDepth {
			break
		}
		if len(rawDepth) != 2 {
			break
		} else {
			qty, _ := strconv.ParseFloat(rawDepth[1], 64)
			price, _ := strconv.ParseFloat(rawDepth[0], 64)
			if qty != 0 && price != 0 {
				isValidDepth = true
				break
			}
		}
	}
	return isValidDepth
}

func convertToTimestamp(timestamp int64) time.Time {
	// Converts raw int64 timestamp into time.Time object.
	seconds := timestamp / 1000
	nanoseconds := (timestamp % 1000) * 1000000
	return time.Unix(seconds, nanoseconds)
}

func IngestAllExchangeBidAsk() {
	/* Runs every 5 minutes to update the websocket connections based on active coins.
	- Starts a new websocket if a new coin is added.
	- Stops existing websocket if an existing coin is removed.
	*/
	var (
		activeCSXGoroutines     map[string]context.CancelFunc = make(map[string]context.CancelFunc)
		activeBinanceGoroutines map[string]context.CancelFunc = make(map[string]context.CancelFunc)
		activeKucoinCancelFunc  context.CancelFunc
		activeKucoinCoins       []string
	)
	// firstTime := true
	for {
		common.ReadWriteMutex.RLock()
		allExchangeCoins := common.AllExchangeCoins
		common.ReadWriteMutex.RUnlock()

		// Manage Coinswitch Websocket Goroutines
		csxCoins, csxExists := allExchangeCoins[common.ExcCoinswitch]
		if csxExists {
			// Stop and remove goroutines for Coinswitch coins that are no longer in the list
			var outDatedCoinswitchCoins []string
			for coin := range activeCSXGoroutines {
				if !common.DoesListContainsString(csxCoins, coin) {
					outDatedCoinswitchCoins = append(outDatedCoinswitchCoins, coin)
				}
			}
			for _, coin := range outDatedCoinswitchCoins {
				common.Log(common.INFO, "Stopping and removing Coinswitch WebSocket goroutine for coin: %s", coin)
				cancelFunc := activeCSXGoroutines[coin]
				cancelFunc()                      // Cancel the goroutine
				delete(activeCSXGoroutines, coin) // Remove from the map
			}

			// Start new goroutines for Coinswitch coins that are newly added
			for _, coin := range csxCoins {
				if _, exists := activeCSXGoroutines[coin]; !exists {
					// if !firstTime {
					// 	common.Log(common.INFO, "Starting new Coinswitch WebSocket goroutine for coin: %s", coin)
					// }
					ctx, cancel := context.WithCancel(context.Background())
					activeCSXGoroutines[coin] = cancel
					go RunCSXWebsocketIngestion(ctx, coin)
				}
			}
		} else {
			common.Log(common.INFO, "Coinswitch is no longer present. Cancelling all active goroutines.")
			// If Coinswitch is no longer present, cancel all active goroutines
			for _, cancelFunc := range activeCSXGoroutines {
				cancelFunc()
			}
			activeCSXGoroutines = make(map[string]context.CancelFunc)
		}

		// Manage Binance Websocket Goroutines
		binanceCoins, binanceExists := allExchangeCoins[common.ExcBinance]
		if binanceExists {
			// Stop and remove goroutines for Binance coins that are no longer in the list
			var outDatedBinanceCoins []string
			for coin := range activeBinanceGoroutines {
				if !common.DoesListContainsString(binanceCoins, coin) {
					outDatedBinanceCoins = append(outDatedBinanceCoins, coin)
				}
			}
			for _, coin := range outDatedBinanceCoins {
				common.Log(common.INFO, "Stopping and removing Binance WebSocket goroutine for coin: %s", coin)
				cancelFunc := activeBinanceGoroutines[coin]
				cancelFunc()                          // Cancel the goroutine
				delete(activeBinanceGoroutines, coin) // Remove from the map
			}
			// Start new goroutines for Binance coins that are newly added
			for _, coin := range binanceCoins {
				if _, exists := activeBinanceGoroutines[coin]; !exists {
					// if !firstTime {
					// 	common.Log(common.INFO, "Starting new Binance WebSocket goroutine for coin: %s", coin)
					// }
					ctx, cancel := context.WithCancel(context.Background())
					activeBinanceGoroutines[coin] = cancel
					go runBinanceWebsocketIngestion(ctx, coin)
				}
			}
		} else {
			common.Log(common.INFO, "Binance is no longer present. Cancelling all active goroutines.")
			// If Binance is no longer present, cancel all active goroutines
			for _, cancelFunc := range activeBinanceGoroutines {
				cancelFunc()
			}
			activeBinanceGoroutines = make(map[string]context.CancelFunc)
		}

		// Manage Kucoin Websocket Goroutines
		// TODO(akul): Improve dynamically turning on/off kucoin websocket.
		kucoinCoins, exists := allExchangeCoins[common.ExcKucoin]
		if exists {
			if !common.AreSlicesEqual(kucoinCoins, activeKucoinCoins) {
				if activeKucoinCancelFunc != nil {
					activeKucoinCancelFunc()
				}
				ctx, cancel := context.WithCancel(context.Background())
				activeKucoinCancelFunc = cancel
				go runKucoinWebsocketIngestion(ctx, kucoinCoins)
				activeKucoinCoins = kucoinCoins
			}
		}

		// firstTime = false
		time.Sleep(5 * time.Minute)
	}
}
