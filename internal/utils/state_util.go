package utils

import (
	"fmt"
	"time"
	"trade_go/internal/api"
	"trade_go/internal/common"
	"trade_go/internal/configs"
)

func UpdateCoinPrecisions(apiClient *api.ApiTradingClient) {
	/* Periodically reads the latest coin precisions.

	- Reads all exchange coin precision using REST api call from csx.
	- Save the latest precisions in JSON file for bookkeeping.
	- Update the global variable with mutex write lock.
	*/
	common.Log(common.INFO, "Starting all exchange COIN PRECISION update...")

	latestCoinPrecisions := fetchAllCoinPrecisions(apiClient, true)
	exchangeWiseCoins := readCoinsFromExchangePrecisions(latestCoinPrecisions)

	common.ReadWriteMutex.Lock()
	common.AllExchangeCoinPrecisions = latestCoinPrecisions
	common.AllExchangeCoins = exchangeWiseCoins
	common.ReadWriteMutex.Unlock()

	commonArbitrageCoins := fetchCommonCoinsAcrossExchanges(apiClient, true)

	common.ReadWriteMutex.Lock()
	common.CommonArbitrageCoins = commonArbitrageCoins
	common.ReadWriteMutex.Unlock()

	for range time.Tick(15 * time.Minute) {

		latestCoinPrecisions = fetchAllCoinPrecisions(apiClient, true)
		exchangeWiseCoins = readCoinsFromExchangePrecisions(latestCoinPrecisions)

		common.ReadWriteMutex.Lock()
		common.AllExchangeCoinPrecisions = latestCoinPrecisions
		common.AllExchangeCoins = exchangeWiseCoins
		common.ReadWriteMutex.Unlock()

		commonArbitrageCoins = fetchCommonCoinsAcrossExchanges(apiClient, true)

		common.ReadWriteMutex.Lock()
		common.CommonArbitrageCoins = commonArbitrageCoins
		common.ReadWriteMutex.Unlock()
	}
}

func updatePortfolioBalance(apiClient *api.ApiTradingClient) {
	/* Updates coin-wise portfolio balance to global variable.

	1. Reads portfolio balance from REST API.
	2. Reads open sell orders and remaining quantity balance.
	*/
	coinsBalance, err := apiClient.GetUserPortfolio()
	if err != nil {
		common.Log(common.ERROR, "fetching portfolio balance\n%v", err)
		return
	}

	params := map[string]interface{}{
		"side":  "sell",
		"open":  true,
		"count": 200, //TODO(akul): Check if the
	}
	openOrders, err := apiClient.GetOpenOrders(params)
	if err != nil {
		common.Log(common.ERROR, "fetching open orders while updating portfolio balance: %v", err)
	}
	for _, order := range openOrders {
		coin, base := common.ExtractCoinNameFromSymbol(order.Symbol)

		// Keep only usable USDT balance, ignore blocked balance.
		if coin == common.BaseUSDT {
			continue
		}

		coinBalanceMeta := coinsBalance[coin]
		coinBalanceMeta.Quantity += order.RemainingQuantity

		if base == common.BaseINR {
			coinBalanceMeta.Amount += (order.Price * order.RemainingQuantity)
		} else {
			priceInInr := ConvertUsdtToInr(order.Price, "average")
			coinBalanceMeta.Amount += (priceInInr * order.RemainingQuantity)
		}
		coinsBalance[coin] = coinBalanceMeta
	}

	common.ReadWriteMutex.Lock()
	common.AccountPortfolioBalance = coinsBalance
	common.ReadWriteMutex.Unlock()
}

func PeriodicUpdatePortfolioBalance(apiClient *api.ApiTradingClient) {
	common.Log(common.INFO, "Starting portfolio balance updater...")
	updatePortfolioBalance(apiClient)
	for range time.Tick(500 * time.Millisecond) {
		updatePortfolioBalance(apiClient)
	}
}

func updateUSDTPrice(apiClient *api.ApiTradingClient) {
	/* Reads latest bidask for stable coins and updates a global variable.
	- Read USDT/INR from redis.
	- Convert to CoinMeta format and save in global variable.
	- Use it to convert the USDT base price into INR.
	*/
	usdtBidAsk, err := GetBidsAndAsks(apiClient, common.BaseUSDT, common.ExcCoinswitch, 1)
	if err != nil {
		common.Log(common.ERROR, "fetching USDT/INR for updating conversion values from %s: `%v`", common.ExcCoinswitch, err)
		return
	}
	usdtPrice := make(map[string]float64)
	usdtPrice["ask"] = usdtBidAsk.Asks[0].Price
	usdtPrice["bid"] = usdtBidAsk.Bids[0].Price
	usdtPrice["average"] = (usdtBidAsk.Bids[0].Price + usdtBidAsk.Asks[0].Price) / 2

	common.ReadWriteMutex.Lock()
	common.UsdtBidAskPrice = usdtPrice
	common.ReadWriteMutex.Unlock()
}

func PeriodicUpdateUSDTPrice(apiClient *api.ApiTradingClient) {
	common.Log(common.INFO, "Starting USDT price conversion updater...")
	updateUSDTPrice(apiClient)
	for range time.Tick(5 * time.Second) {
		updateUSDTPrice(apiClient)
	}
}

func PeriodicUpdateRedisQuantity(apiClient *api.ApiTradingClient) {
	common.Log(common.INFO, "Starting all coins redis quantity updater...")
	UpdatePortfolioBalanceInRedis(apiClient, true)
	for range time.Tick(10 * time.Minute) {
		UpdatePortfolioBalanceInRedis(apiClient, true)
	}
}

func ReadCachedCoinBalance(coin string) float64 {
	common.ReadWriteMutex.RLock()
	coinBalanceMetadata := common.AccountPortfolioBalance[coin]
	common.ReadWriteMutex.RUnlock()
	return coinBalanceMetadata.Amount
}

func PeriodicSellPortfolioBalance(apiClient *api.ApiTradingClient) {
	common.Log(common.INFO, "Starting Automated SELLING of portfolio balance...")
	for {
		placePortfolioSellOrder(apiClient)
		time.Sleep(5 * time.Second)
	}
}

func PeriodicCheckOpenSellOrders(apiClient *api.ApiTradingClient) {
	common.Log(common.INFO, "Checking for open sell orders ...")
	for {
		processOpenOrders(apiClient)
		time.Sleep(5 * time.Second)
	}
}

func UpdatePortfolioBalanceInRedis(apiClient *api.ApiTradingClient, updateForDifference bool) {
	/* Updates the buy average price and quantity for coins in redis.
	- Only update for coins whose quantity is not equal to the quantity from API.
	*/
	common.Log(common.INFO, "Updating inconsistent quantity and buyAveragePrice in redis...")
	allCoinPortfolioBalance, err := apiClient.GetUserPortfolio()
	if err != nil {
		common.Log(common.ERROR, "reading portfolio balance: `%v`", err)
		return
	}
	for coin, portfolioMeta := range allCoinPortfolioBalance {
		if common.DoesListContainsString(configs.NotTradedCoins, coin) {
			continue
		}
		if coin == common.BaseINR {
			continue
		}
		portfolioQuantity := portfolioMeta.Quantity
		portfolioAvgBuyPrice := portfolioMeta.Amount / portfolioQuantity

		// if portfolioQuantity*portfolioAvgBuyPrice < 100 {
		// 	continue
		// }

		redisAvgBuyPrice, redisQuantity := common.ReadAvgBuyPriceFromRedis(coin)

		if redisQuantity != portfolioQuantity {
			if !updateForDifference {
				if portfolioQuantity == 0 {
					common.SaveAvgBuyPriceToRedis(coin, 0, 0)
					continue
				}
				lifoAvgBuyPrice := 0.0
				if portfolioQuantity > 0 {
					lifoAvgBuyPrice = calculateAvgBuyPriceUsingLIFO(apiClient, coin, portfolioAvgBuyPrice, portfolioQuantity)
				}

				common.SaveAvgBuyPriceToRedis(coin, 0, 0)
				common.Log(common.INFO, "%s: updated AvgBuyPrice and Quantity for in redis: Quantity: %f -> portfolioBuyPrice: %f -> lifoBuyPrice: %f", coin, portfolioMeta.Quantity, portfolioAvgBuyPrice, lifoAvgBuyPrice)
				UpdateAvgBuyPrice(coin, lifoAvgBuyPrice, portfolioQuantity, true)
			} else {
				if redisQuantity > portfolioQuantity {
					quantityDifference := redisQuantity - portfolioQuantity
					common.Log(common.WARNING, "matching actual %s quantity, REDUCE : difference: %f", coin, quantityDifference)
					UpdateAvgBuyPrice(coin, redisAvgBuyPrice, quantityDifference, false)

				} else if redisQuantity < portfolioQuantity {
					quantityDifference := portfolioQuantity - redisQuantity
					lifoAvgBuyPrice := calculateAvgBuyPriceUsingLIFO(apiClient, coin, portfolioAvgBuyPrice, quantityDifference)

					common.Log(common.WARNING, "matching actual %s quantity, ADD - difference: %f", coin, quantityDifference)
					UpdateAvgBuyPrice(coin, lifoAvgBuyPrice, quantityDifference, true)
				}
			}
		}

	}
}

func RefreshOpenSellOrderIdInRedis(apiClient *api.ApiTradingClient) {
	/* Updates existing open sell order ids.
	- Read all open sell orders from REST API.
	- For each order id, check if the order id exists:
		- If yes, do nothing.
		- If no, cancel the open sell order.
	*/
	common.Log(common.INFO, "Updating open sell order ids in redis...")
	common.ReadWriteMutex.RLock()
	allCoinPortfolioBalance := common.AccountPortfolioBalance
	common.ReadWriteMutex.RUnlock()

	for coin, coinBalance := range allCoinPortfolioBalance {
		if common.DoesListContainsString(configs.NotTradedCoins, coin) {
			continue
		}
		if coinBalance.Amount < common.MinValidOrderAmountInrBase {
			continue
		}
		redisSellOrders := common.ReadOrderDetailsFromRedis(coin)
		openSellOrders, err := apiClient.GetOpenOrders(map[string]interface{}{
			"side":    "sell",
			"symbols": fmt.Sprintf("%s/%s,%s/%s", coin, common.BaseINR, coin, common.BaseUSDT),
			"count":   200,
		})
		if err != nil {
			common.Log(common.ERROR, "fetching open sell orders: `%v`", err)
			continue
		}
		for _, order := range openSellOrders {
			orderId := order.Id
			_, exists := redisSellOrders[orderId]
			if !exists {
				_ = CancelOrderById(apiClient, orderId)
			}
		}
	}
}

func PeriodicUpdateCommissionFee(apiClient *api.ApiTradingClient) {
	exchangeWiseCommission, err := apiClient.GetTradingFee()
	common.Log(common.INFO, "Latest commissions: %v", exchangeWiseCommission)
	if err != nil {
		common.Log(common.ERROR, "reading trading fees: %v", err)
	} else {
		common.ReadWriteMutex.Lock()
		common.CommissionFee = exchangeWiseCommission
		common.ReadWriteMutex.Unlock()
	}
	for range time.Tick(10 * time.Minute) {
		exchangeWiseCommission, err := apiClient.GetTradingFee()
		if err != nil {
			common.Log(common.ERROR, "reading trading fees: %v", err)
		} else {
			common.ReadWriteMutex.Lock()
			common.CommissionFee = exchangeWiseCommission
			common.ReadWriteMutex.Unlock()

		}
	}
}

func LogTDSInfo(apiClient *api.ApiTradingClient) {
	if configs.AccountType != "regular" {
		return
	}
	tdsAmount, err := apiClient.GetTDSInfo()
	if err != nil {
		common.Log(common.ERROR, "fetching TDS info: `%v`", err)
	} else {
		common.Log(common.INFO, "Latest TDS : %f INR", tdsAmount)
	}

	for range time.Tick(15 * time.Minute) {
		tdsAmount, err := apiClient.GetTDSInfo()
		if err != nil {
			common.Log(common.ERROR, "fetching TDS info: `%v`", err)
			continue
		}
		common.Log(common.INFO, "Latest TDS : %f INR", tdsAmount)
	}
}

func PeriodicCheckBotStoppingCriteria(apiClient *api.ApiTradingClient) {
	var sufficientBalance bool
	sufficientBalance = isCommissionInAccountSufficient(apiClient)
	if !sufficientBalance {
		return
	}
	ticker := time.NewTicker(2 * time.Minute)
	for range ticker.C {
		sufficientBalance = isCommissionInAccountSufficient(apiClient)
		if !sufficientBalance {
			return
		}
	}
}

func AddBlackListedCoin(apiClient *api.ApiTradingClient) {
	coins := apiClient.GetBlackistedCoins()
	configs.NotTradedCoins = append(configs.NotTradedCoins, coins...)
}
