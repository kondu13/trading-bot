package arbitrage

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"
	"trade_bot/internal/api"
	"trade_bot/internal/common"
	"trade_bot/internal/configs"
	"trade_bot/internal/utils"
)

func findValidOfferByValue(offers []common.CoinMeta) *common.CoinMeta {
	// Finds valid depth which satisfies min order amount.
	for _, offer := range offers {
		if common.IsValidOfferByOrderAmount(&offer) {
			return &offer
		}
	}
	return nil
}

func calculateArbitrage(buyOffer, sellOffer *common.CoinMeta, minProfitPercent, maxProfitPercent float64) (float64, bool) {
	/*
		Following cases can occur:
		1. BUY: USDT base and SELL: USDT base -> "redis -> bid"
		2. BUY: USDT base and SELL: csx -> "redis price"
		3. BUY: csx and SELL: USDT base -> "bid price"
	*/
	isBuyUSDTExchange := common.IsUSDTBaseExchange(buyOffer.Exchange)
	isSellUSDTExchange := common.IsUSDTBaseExchange(sellOffer.Exchange)

	buyPrice := buyOffer.Price
	if isBuyUSDTExchange {
		// TODO(akul): Fix the usdt price in redis logic.
		// buyPrice = buyOffer.PriceWithUsdtRedis
		buyPrice = buyOffer.PriceWithUsdtAsk
	}
	sellPrice := sellOffer.Price
	if isSellUSDTExchange {
		sellPrice = sellOffer.PriceWithUsdtBid
	}

	common.ReadWriteMutex.RLock()
	buyCommissionFee := common.CommissionFee[buyOffer.Exchange]
	sellCommissionFee := common.CommissionFee[sellOffer.Exchange]
	common.ReadWriteMutex.RUnlock()

	tradeQuantity := buyOffer.Quantity

	// Total Profit before commission.
	profit := (sellPrice - buyPrice) * tradeQuantity

	// Buy + Sell commission : Based on the total volume traded each side.
	commissionAmount := (sellCommissionFee*sellPrice + buyCommissionFee*buyPrice) * tradeQuantity

	// Total buy price : asset price + commission
	spendingAmount := (buyPrice)*tradeQuantity + commissionAmount

	netProfit := profit - commissionAmount
	marginOfSpendingAmount := netProfit / spendingAmount

	isViableTrade := (marginOfSpendingAmount >= minProfitPercent) && (marginOfSpendingAmount <= maxProfitPercent)

	return netProfit, isViableTrade
}

func calculateTradeQuantity(coin string, quantity float64, buyExchange string, sellExchange string) float64 {
	common.ReadWriteMutex.RLock()
	buyPrecisions := (common.AllExchangeCoinPrecisions[buyExchange][coin])
	sellPrecisions := (common.AllExchangeCoinPrecisions[sellExchange][coin])
	common.ReadWriteMutex.RUnlock()

	var buyPrecision, sellPrecision float64

	if buyPrecisions == nil {
		common.Log(common.ERROR, "getting precision for %s on %s.", coin, buyExchange)
		buyPrecision = 0
	} else {
		buyPrecision = buyPrecisions["base"]
	}
	if sellPrecisions == nil {
		common.Log(common.ERROR, "getting precision for %s on %s.", coin, sellExchange)
		sellPrecision = 0
	} else {
		sellPrecision = sellPrecisions["base"]
	}

	minQuantityPrecision := min(buyPrecision, sellPrecision)
	factor := math.Pow(10, minQuantityPrecision)
	return math.Trunc(quantity*factor) / factor
}

func isExistingBestArbitrage(buyOffer common.CoinMeta, sellOffer common.CoinMeta, bestBuyOffer common.CoinMeta, bestSellOffer common.CoinMeta) bool {
	isUsdtBuyOffer := common.IsUSDTBaseExchange(buyOffer.Exchange)
	isUsdtSellOffer := common.IsUSDTBaseExchange(sellOffer.Exchange)
	isUsdtBestBuyOffer := common.IsUSDTBaseExchange(bestBuyOffer.Exchange)
	isUsdtBestSellOffer := common.IsUSDTBaseExchange(bestSellOffer.Exchange)
	if !isUsdtBestBuyOffer && !isUsdtBestSellOffer {
		return true
	}
	if !isUsdtBuyOffer && !isUsdtSellOffer {
		return false
	}
	if isUsdtBestBuyOffer && isUsdtBestSellOffer {
		return true
	}
	if isUsdtBuyOffer && isUsdtSellOffer {
		return false
	}
	return true
}

func fetchArbitrageBuySellOffers(apiClient *api.ApiTradingClient, coin string) (map[string]common.CoinMeta, float64) {

	exchangeWiseBidsAndAsks := utils.FetchExchangeWiseBidAskInSequence(apiClient, coin)

	var validExchanges []string
	for exchange, _ := range exchangeWiseBidsAndAsks {
		validExchanges = append(validExchanges, exchange)
	}

	buySellExchangeCombinations := common.PermutationOverListElements(validExchanges)

	tradeBuyOffer := common.CoinMeta{}
	tradeSellOffer := common.CoinMeta{}
	var bestArbitrageProfit float64

	for _, buySellExchanges := range buySellExchangeCombinations {
		buyExchange := buySellExchanges[0]
		sellExchange := buySellExchanges[1]

		validBuyOffer := findValidOfferByValue(exchangeWiseBidsAndAsks[buyExchange].Asks)
		validSellOffer := findValidOfferByValue(exchangeWiseBidsAndAsks[sellExchange].Bids)

		if validBuyOffer == nil || validSellOffer == nil {
			continue
		}
		buyBaseCoin := common.BaseINR
		baseBalanceOrderAmountPercent := configs.INRBaseTradeAmountPercent
		if common.IsUSDTBaseExchange(buyExchange) {
			buyBaseCoin = common.BaseUSDT
			baseBalanceOrderAmountPercent = configs.USDTBaseTradeAmountPercent
		}

		buyBaseBalance := utils.ReadCachedCoinBalance(buyBaseCoin)
		maxOrderAmount := buyBaseBalance * baseBalanceOrderAmountPercent

		// experimental
		// maxOrderAmount = 1000

		maxTradeQuantity := maxOrderAmount / validBuyOffer.Price

		tradeQuantity := min(maxTradeQuantity, validBuyOffer.Quantity, validSellOffer.Quantity)
		tradeQuantity = calculateTradeQuantity(coin, tradeQuantity, validBuyOffer.Exchange, validSellOffer.Exchange)
		validBuyOffer.Quantity = tradeQuantity
		validSellOffer.Quantity = tradeQuantity

		netProfit, isViableTrade := calculateArbitrage(validBuyOffer, validSellOffer, configs.MinProfitPercentOfSpendingAmount, configs.MaxProfitPercentOfSpendingAmount)

		// tradeMargin := (validSellOffer.Price - validBuyOffer.Price) / validBuyOffer.Price
		// tradeAmount := validBuyOffer.Price * validBuyOffer.Quantity
		// common.Log(common.INFO, "%s: arbitrage -> Margin: %.4f -> TradeAmount: %.3f/- (Net Profit): INR: %.3f/- -> (BUY on %s, price: %f) -> (SELL on %s, price: %f) -> qty: %f", coin, tradeMargin, tradeAmount, netProfit, validBuyOffer.Exchange, validBuyOffer.Price, validSellOffer.Exchange, validSellOffer.Price, validBuyOffer.Quantity)

		if isViableTrade && netProfit >= MinArbitrageProfit && netProfit >= bestArbitrageProfit {
			if netProfit == bestArbitrageProfit && isExistingBestArbitrage(*validBuyOffer, *validSellOffer, tradeBuyOffer, tradeSellOffer) {
				continue
			}
			if common.IsValidOfferByOrderAmount(validBuyOffer) && common.IsValidOfferByOrderAmount(validSellOffer) {
				tradeBuyOffer = *validBuyOffer
				tradeSellOffer = *validSellOffer
				bestArbitrageProfit = netProfit
			}
		}
	}
	return map[string]common.CoinMeta{"ask": tradeBuyOffer, "bid": tradeSellOffer}, bestArbitrageProfit
}

func placeArbitrageSellOrder(apiClient *api.ApiTradingClient, buyPrice float64, sellOffer common.CoinMeta) bool {
	/*
		Place SELL order and store the orderId and buyPrice in the redis
	*/
	sellOrderID, err := utils.PlaceOrder(apiClient, sellOffer, "sell")
	if err == nil && sellOrderID != "" {
		common.AppendOrderDetailsToRedis(sellOffer.Coin, sellOrderID, buyPrice)
		utils.UpdateAvgBuyPrice(sellOffer.Coin, buyPrice, sellOffer.Quantity, false)
		return true
	}
	return false
}

func completeArbitrageTrade(apiClient *api.ApiTradingClient, buyOffer common.CoinMeta, sellOffer common.CoinMeta) bool {
	/* Places the buy order and wait for the order to fulfill.
	1. Place buy order.
	2. Wait until the following conditions are all true:
		- buy order is not closed
		- marketBuyPrice >= buyOffer price.
		- marketSellPrice <= sellOffer price.
		- Wait time is under some max limit.
	3. Place sell order if the following conditions are all true:
		- buy order is closed.
		- marketSellPrice <= sellOffer price.
	4. In case we are not able to place the sell offer, do the following:
		- Update BuyAveragePrice and coinQuantity in redis.
		- Cancel open buy order.
	*/
	buyOrderID, err := utils.PlaceOrder(apiClient, buyOffer, "buy")
	if err == nil && buyOrderID != "" {
		var isOrderClosed bool
		var topSellPrice float64
		var topBuyPrice float64
		var executedQuantity float64
		var bidAskBuyExchange common.BidAskDepth
		var bidAskSellExchange common.BidAskDepth
		startTime := time.Now()
		for {
			time.Sleep(500 * time.Millisecond)
			isOrderClosed, _ = utils.IsOrderClosed(apiClient, buyOrderID)
			bidAskBuyExchange, _ = utils.GetBidsAndAsks(apiClient, buyOffer.Coin, buyOffer.Exchange, 1)
			bidAskSellExchange, _ = utils.GetBidsAndAsks(apiClient, sellOffer.Coin, sellOffer.Exchange, 1)
			topBuyPrice = bidAskBuyExchange.Asks[0].Price
			topSellPrice = bidAskSellExchange.Bids[0].Price
			if (isOrderClosed) || (topBuyPrice > buyOffer.Price) || (topSellPrice < sellOffer.Price) || (time.Since(startTime).Seconds() > common.MaxWaitingTime) {
				break
			}
		}
		orderData, _ := apiClient.GetOrderById(map[string]interface{}{"order_id": buyOrderID})
		isOrderClosed = orderData.Status == "EXECUTED"
		if !isOrderClosed {
			orderData = utils.CancelOrderById(apiClient, buyOrderID)
		}

		// Update the consumed quantity of USDT in closing buy order on USDT base exchange.
		if common.IsUSDTBaseExchange(buyOffer.Exchange) && orderData.ExecutedQuantity > 0 {
			common.ReadWriteMutex.RLock()
			buyCommissionFee := common.CommissionFee[buyOffer.Exchange]
			usdtBidAskPrice := common.UsdtBidAskPrice
			common.ReadWriteMutex.RUnlock()

			var usdtUsed float64

			if isOrderClosed {
				usdtUsed = orderData.Price * orderData.OriginalQuantity
			} else {
				tradeVolume := orderData.Price * orderData.ExecutedQuantity
				usdtCommissionAmount := tradeVolume * buyCommissionFee
				usdtTDSAmount := tradeVolume * common.TDSDetectionPercent
				usdtUsed = tradeVolume + usdtCommissionAmount + usdtTDSAmount
			}
			utils.UpdateAvgBuyPrice(common.BaseUSDT, usdtBidAskPrice["bid"], usdtUsed, false)
		}

		executedQuantity = orderData.ExecutedQuantity
		if executedQuantity > 0 {
			common.Log(common.INFO, "partial order for %s executed: %f/%f quantity", orderData.Symbol, executedQuantity, orderData.OriginalQuantity)
			utils.UpdateAvgBuyPrice(buyOffer.Coin, buyOffer.Price, executedQuantity, true)

			bidAskSellExchange, _ = utils.GetBidsAndAsks(apiClient, sellOffer.Coin, sellOffer.Exchange, 1)
			topSellPrice = bidAskSellExchange.Bids[0].Price
			if topSellPrice >= sellOffer.Price {
				sellOffer.Quantity = utils.ConvertToPreciseQuantity(executedQuantity, sellOffer.Coin, sellOffer.Exchange)
				return placeArbitrageSellOrder(apiClient, buyOffer.Price, sellOffer)
			}
		}
	} else {
		common.Log(common.ERROR, "placing BUY order for arbitrage trade: %s : %s -> price: %f -> qty: %f", buyOffer.Coin, buyOffer.Exchange, buyOffer.Price, buyOffer.Quantity)
	}
	return false
}

func runArbitrageForSingleCoin(ctx context.Context, apiClient *api.ApiTradingClient, coin string) {
	if common.DoesListContainsString(configs.NotTradedCoins, coin) {
		return
	}
	frequency := 1 * time.Second
	for {
		// Check if the context has been canceled
		if ctx.Err() != nil {
			// Cleanup and exit the goroutine
			return
		}
		time.Sleep(frequency)

		common.ReadWriteMutex.RLock()
		inrBalanceMetadata := common.AccountPortfolioBalance[common.BaseINR]
		coinBalanceMetadata := common.AccountPortfolioBalance[coin]
		common.ReadWriteMutex.RUnlock()

		maxPossibleOrderAmount := min(
			configs.USDTBaseTradeAmountPercent*inrBalanceMetadata.Amount,
			configs.INRBaseTradeAmountPercent*inrBalanceMetadata.Amount,
		)
		if coinBalanceMetadata.Amount > maxPossibleOrderAmount {
			common.Log(common.WARNING, "skipping arbitrage trade for `%s` because portfolio balance: `%.3f`/- and min order amount: `%.3f`/-", coin, coinBalanceMetadata.Amount, maxPossibleOrderAmount)
			frequency = 5 * time.Second
			continue
		}
		frequency = 1 * time.Second

		validOrders, netProfit := fetchArbitrageBuySellOffers(apiClient, coin)
		if netProfit >= MinArbitrageProfit {
			buyOffer := validOrders["ask"]
			sellOffer := validOrders["bid"]

			// Using for logging.
			tradeMargin := (sellOffer.Price - buyOffer.Price) / buyOffer.Price
			tradeAmount := buyOffer.Price * buyOffer.Quantity

			common.Log(common.INFO, "%s: arbitrage -> Margin: %.4f -> TradeAmount: %.3f/- (Net Profit): INR: %.3f/- -> (BUY on %s, price: %f) -> (SELL on %s, price: %f) -> qty: %f", coin, tradeMargin, tradeAmount, netProfit, buyOffer.Exchange, buyOffer.Price, sellOffer.Exchange, sellOffer.Price, buyOffer.Quantity)

			if buyOffer.Quantity != 0 {
				isSuccessfulTrade := completeArbitrageTrade(apiClient, buyOffer, sellOffer)
				if !isSuccessfulTrade {
					common.Log(common.ERROR, "%s: FAILED arbitrage trade.", coin)
				}
			}
		}

	}
}

func calculateSingleCoinArbitrageOpportunity(
	wg *sync.WaitGroup,
	arbitrageMetadataChan chan<- common.ArbitrageOpportunity,
	apiClient *api.ApiTradingClient,
	coin, buyExchange, sellExchange string,
	maxTradeAmount, minProfitPercent, minProfit float64,
) {
	/* Reads all possible arbitrage opportunity for a specific buy/sell exchanges.
	1. For all common coins, checks the latest bidask price.
	2. In parallel: calculates possible arbitrage margin and amount.
	3. Returns all possible arbitrage opportunity after sorting by the quantity*margin value.
	*/
	defer wg.Done()
	arbitrageMetadata := common.ArbitrageOpportunity{}
	buyExchangeBidAsk, err := utils.GetBidsAndAsks(apiClient, coin, buyExchange, 1)
	if err != nil {
		return
	}
	sellExchangeBidAsk, err := utils.GetBidsAndAsks(apiClient, coin, sellExchange, 1)
	if err != nil {
		return
	}
	validBuyOffer := findValidOfferByValue(buyExchangeBidAsk.Asks)
	validSellOffer := findValidOfferByValue(sellExchangeBidAsk.Bids)
	if validBuyOffer == nil || validSellOffer == nil {
		return
	}

	maxTradeQuantity := maxTradeAmount / validBuyOffer.Price
	tradeQuantity := min(maxTradeQuantity, validBuyOffer.Quantity, validSellOffer.Quantity)
	tradeQuantity = calculateTradeQuantity(coin, tradeQuantity, validBuyOffer.Exchange, validSellOffer.Exchange)

	validBuyOffer.Quantity = tradeQuantity
	validSellOffer.Quantity = tradeQuantity
	netProfit, isViableTrade := calculateArbitrage(validBuyOffer, validSellOffer, minProfitPercent, 100)

	if netProfit >= minProfit && isViableTrade && common.IsValidOfferByOrderAmount(validBuyOffer) && common.IsValidOfferByOrderAmount(validSellOffer) {
		arbitrageMetadata.BuyOffer = *validBuyOffer
		arbitrageMetadata.SellOffer = *validSellOffer
		arbitrageMetadata.NetProfit = netProfit
		arbitrageMetadataChan <- arbitrageMetadata
	}
}

func FetchAllCoinArbitrageOpportunity(
	apiClient *api.ApiTradingClient,
	buyExchange, sellExchange string,
	maxTradeAmount, minProfitPercent, minProfit float64,
) []common.ArbitrageOpportunity {

	common.ReadWriteMutex.RLock()
	commonArbitrageCoins := common.CommonArbitrageCoins
	common.ReadWriteMutex.RUnlock()

	var wg sync.WaitGroup
	wg.Add(len(commonArbitrageCoins))

	arbitrageMetadataChan := make(chan common.ArbitrageOpportunity, len(commonArbitrageCoins))

	for _, coin := range commonArbitrageCoins {
		if common.DoesListContainsString(configs.NotTradedCoins, coin) {
			continue
		}
		go calculateSingleCoinArbitrageOpportunity(&wg, arbitrageMetadataChan, apiClient, coin, buyExchange, sellExchange, maxTradeAmount, minProfitPercent, minProfit)
	}
	wg.Wait()
	close(arbitrageMetadataChan)

	var allArbitrageOpportunities []common.ArbitrageOpportunity

	for result := range arbitrageMetadataChan {
		allArbitrageOpportunities = append(allArbitrageOpportunities, result)
	}

	sort.Slice(allArbitrageOpportunities, func(i, j int) bool {
		return allArbitrageOpportunities[i].NetProfit > allArbitrageOpportunities[j].NetProfit
	})

	return allArbitrageOpportunities
}
