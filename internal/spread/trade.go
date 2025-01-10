/*
Strategy 1:
- BUY from csx "bid" amount : Limit order.
- Wait until conditions are favourable.
- SELL at binance "bid" : Market order.

Strategy 2:
- BUY from binance "ask" : market order.
- SELL at csx "ask" : limit order.
- Keep the sell order on top of ask of csx.
*/

package spread

import (
	"log"
	"math"
	"sort"
	"time"
	"trade_go/internal/api"
	"trade_go/internal/arbitrage"
	"trade_go/internal/common"
	"trade_go/internal/configs"
	"trade_go/internal/utils"
)

func initializeSpread1ForCoin(apiClient *api.ApiTradingClient, coin string) {
	log.Printf("Starting SPREAD-1 trading for %s", coin)

	if common.DoesListContainsString(configs.NotTradedCoins, coin) {
		return
	}

	for {
		common.ReadWriteMutex.RLock()
		latestSpread1Coins := common.Spread1Coins
		common.ReadWriteMutex.RUnlock()

		if !common.DoesListContainsString(latestSpread1Coins, coin) {
			common.Log(common.INFO, "Stopping SPREAD-1 for %s due to updated spread-1 coins.", coin)
			return
		}

		coinBalance := utils.ReadCachedCoinBalance(coin)

		tradeAmount := configs.Spread1DefaultOrderAmount
		coinOrderAmount, exists := configs.Spread1OrderAmount[coin]
		if exists {
			if coinOrderAmount > 0 {
				tradeAmount = coinOrderAmount
			}
		}

		if coinBalance < tradeAmount {
			sellFlag, buyOffer := placeSpread1BuyOrder(apiClient, coin, tradeAmount)
			if sellFlag {
				placeSpread1SellOrder(apiClient, coin, buyOffer)
			}
		}

		time.Sleep(1 * time.Second)
	}
}

func initializeSpread2ForCoin(apiClient *api.ApiTradingClient, coin string) {
	log.Printf("Starting SPREAD-2 trading for %s", coin)

	if common.DoesListContainsString(configs.NotTradedCoins, coin) {
		return
	}

	for {
		coinBalance := utils.ReadCachedCoinBalance(coin)

		tradeAmount := configs.Spread2DefaultOrderAmount
		coinOrderAmount, exists := configs.Spread2OrderAmount[coin]
		if exists {
			if coinOrderAmount > 0 {
				tradeAmount = coinOrderAmount
			}
		}

		if coinBalance < tradeAmount {
			placeSpread2BuyOrder(apiClient, coin, tradeAmount)
		}
		time.Sleep(1 * time.Second)
	}
}

func StartSpread1Trade(apiClient *api.ApiTradingClient) {
	common.Log(common.INFO, "Starting spread-1 trading...")
	for _, coin := range common.Spread1Coins {
		if !common.DoesListContainsString(configs.NotTradedCoins, coin) {
			go initializeSpread1ForCoin(apiClient, coin)
		}
	}
}
func StartSpread2Trade(apiClient *api.ApiTradingClient) {
	common.Log(common.INFO, "Starting spread-2 trading...")
	for _, coin := range configs.Spread2Coins {
		if !common.DoesListContainsString(configs.NotTradedCoins, coin) {
			go initializeSpread2ForCoin(apiClient, coin)
		}
	}
}

// Updates SPREAD-1 coins.
func updateSpread1Coins(apiClient *api.ApiTradingClient) bool {
	/*
		1. Check the coins with positive arbitrage.
		2. Check the trading amount based on portfolio balance.
		3. Close the goroutines if the coins are removed from the spread coins.
	*/
	common.ReadWriteMutex.RLock()
	existingSpread1Coins := common.Spread1Coins
	common.ReadWriteMutex.RUnlock()

	binanceSpread1Coins := arbitrage.FetchAllCoinArbitrageOpportunity(
		apiClient,
		common.ExcCoinswitch,
		common.ExcBinance,
		math.Inf(1),
		configs.MinArbitrageForSpread1Coins,
		0,
	)
	kucoinSpread1Coins := arbitrage.FetchAllCoinArbitrageOpportunity(
		apiClient,
		common.ExcCoinswitch,
		common.ExcKucoin,
		math.Inf(1),
		configs.MinArbitrageForSpread1Coins,
		0,
	)

	allSpread1Coins := append(binanceSpread1Coins, kucoinSpread1Coins...)

	sort.Slice(allSpread1Coins, func(i, j int) bool {
		return allSpread1Coins[i].NetProfit > allSpread1Coins[j].NetProfit
	})

	newSpread1CoinList := []string{}
	for _, opportunity := range allSpread1Coins {
		if len(newSpread1CoinList) >= configs.Spread1CoinCount {
			break
		}
		coin := opportunity.BuyOffer.Coin
		if !common.DoesListContainsString(configs.NotTradedCoins, coin) &&
			!common.DoesListContainsString(newSpread1CoinList, coin) {
			newSpread1CoinList = append(newSpread1CoinList, coin)
		}
	}

	if len(newSpread1CoinList) > 0 {
		common.Log(common.INFO, "Latest spread-1 coins: %v", newSpread1CoinList)
		common.ReadWriteMutex.Lock()
		common.Spread1Coins = newSpread1CoinList
		common.ReadWriteMutex.Unlock()
		for _, coin := range newSpread1CoinList {
			if !common.DoesListContainsString(existingSpread1Coins, coin) {
				go initializeSpread1ForCoin(apiClient, coin)
			}
		}
		return true
	}
	return false
}

// Updates SPREAD-1 coins.
func PeriodicUpdateSpread1Coins(apiClient *api.ApiTradingClient) {
	common.Log(common.INFO, "Starting spread1 coin updater...")
	frequency := 3 * time.Minute
	foundCoins := updateSpread1Coins(apiClient)
	if !foundCoins {
		frequency = 1 * time.Minute
	}
	for range time.Tick(frequency) {
		foundCoins = updateSpread1Coins(apiClient)
		if !foundCoins {
			frequency = 1 * time.Minute
		} else {
			frequency = 3 * time.Minute
		}
	}
}

func CancelSpread1StaleBuyOrders(apiClient *api.ApiTradingClient, cancelAll bool) {
	/*
		1. Fetch open buy orders.
		2. Check if the order is from spread-1 and present in the redis.
		3. Cancel the order and remove order id from redis.
	*/
	common.Log(common.INFO, "Canceling open buy orders from spread-1 ...")
	openBuyOrders, err := apiClient.GetOpenOrders(map[string]interface{}{
		"side":  "buy",
		"count": 200,
	})
	common.Log(common.INFO, "%v", openBuyOrders)
	if err != nil {
		common.Log(common.ERROR, "failed to cancel open BUY spread-1 orders: `%v`", err)
	}
	for _, buyOrder := range openBuyOrders {
		orderId := buyOrder.Id
		coin, _ := common.ExtractCoinNameFromSymbol(buyOrder.Symbol)
		existingBuyOrderId := common.ReadSpreadOrderIdFromRedis(coin)
		if existingBuyOrderId == orderId {
			_ = utils.CancelOrderById(apiClient, existingBuyOrderId)
			common.RemoveSpreadOrderIdFromRedis(coin)
			common.Log(common.WARNING, "%s:%s cancelled spread-1 BUY order", coin, orderId)
		} else if cancelAll {
			_ = utils.CancelOrderById(apiClient, orderId)
		} else {
			common.Log(common.WARNING, "open BUY order not cancelled: %v", buyOrder)
		}
	}
}
