package arbitrage

import (
	"sync"
	"time"
	"trade_bot/internal/api"
	"trade_bot/internal/common"
	"trade_bot/internal/utils"
)

func calculateUSDTBalanceLimits() (float64, float64) {
	common.ReadWriteMutex.RLock()
	inrBalanceMetadata := common.AccountPortfolioBalance[common.BaseINR]
	usdtBalanceMetadata := common.AccountPortfolioBalance[common.BaseUSDT]
	common.ReadWriteMutex.RUnlock()

	baseCoinBalance := inrBalanceMetadata.Amount + usdtBalanceMetadata.Amount
	minUSDTBalance := baseCoinBalance * USDTMinBalanceINRRatio
	maxUSDTBalance := baseCoinBalance * USDTMaxBalanceINRRatio
	common.Log(common.INFO, "Latest USDT limits : %.3f < USDT < %.3f", minUSDTBalance, maxUSDTBalance)
	return minUSDTBalance, maxUSDTBalance
}

func determineRebalanceSide(base string) (string, float64) {
	lowerLimit, upperLimit := calculateUSDTBalanceLimits()

	common.ReadWriteMutex.RLock()
	portfolioQuant := common.AccountPortfolioBalance[base].Quantity
	usdtMarketPrices := (common.UsdtBidAskPrice["bid"] + common.UsdtBidAskPrice["ask"]) / 2
	common.ReadWriteMutex.RUnlock()

	existingAmount := portfolioQuant * usdtMarketPrices
	common.Log(common.INFO, "Existing USDT balance: %.3f -> Quantity: %.3f", existingAmount, portfolioQuant)

	side := "neutral"
	tradeAmount := 0.0
	if existingAmount > upperLimit {
		side = "sell"
		tradeAmount = existingAmount - upperLimit
	} else if existingAmount < lowerLimit {
		side = "buy"
		tradeAmount = lowerLimit - existingAmount
	}

	return side, tradeAmount
}

func completeRebalanceArbitrageTrade(wg *sync.WaitGroup, apiClient *api.ApiTradingClient, buyOffer common.CoinMeta, sellOffer common.CoinMeta) bool {
	defer wg.Done()
	tradeMargin := (sellOffer.Price - buyOffer.Price) / buyOffer.Price
	common.Log(common.INFO, "%s: valid arbitrage for rebalancing USDT -> %.4f -> (BUY on %s, price: %f) -> (SELL on %s, price: %f) -> qty: %f", buyOffer.Coin, tradeMargin, buyOffer.Exchange, buyOffer.Price, sellOffer.Exchange, sellOffer.Price, buyOffer.Quantity)

	return completeArbitrageTrade(apiClient, buyOffer, sellOffer)
}

func rebalanceUSDT(apiClient *api.ApiTradingClient) {
	base := common.BaseUSDT
	side, maxTradeAmount := determineRebalanceSide(base)
	if maxTradeAmount < common.MinValidOrderAmount || side == "neutral" {
		return
	}
	var buyExchanges, sellExchanges []string
	if side == "buy" {
		buyExchanges = []string{common.ExcCoinswitch}
		sellExchanges = []string{common.ExcBinance}
	} else if side == "sell" {
		buyExchanges = []string{common.ExcBinance}
		sellExchanges = []string{common.ExcCoinswitch}
	}

	common.Log(common.INFO, "Rebalance %s : %s -> Amount: %.3f", base, side, maxTradeAmount)
	currentSide := side
	maxRetries := 5
	rebalanceRequired := true

	var wg sync.WaitGroup

	// Tries to find the arbitrage opportunity 5 times in 25 seconds and then place market order.
	for currentSide == side && rebalanceRequired && maxTradeAmount > common.MinValidOrderAmount && maxRetries > 0 {
		arbitrageOpportunityList := FetchAllCoinArbitrageOpportunity(apiClient, buyExchanges[0], sellExchanges[0], maxTradeAmount, USDTRebalanceMinMargin, MinRebalanceUSDTProfit)
		common.Log(common.INFO, "Number of arbitrage opportunities for rebalancing USDT: %v - Retries remaining: %v", len(arbitrageOpportunityList), maxRetries)
		for _, arbitrageOpportunity := range arbitrageOpportunityList {
			if maxTradeAmount < common.MinValidOrderAmount {
				break
			}

			validBuyOffer := arbitrageOpportunity.BuyOffer
			validSellOffer := arbitrageOpportunity.SellOffer
			coin := validBuyOffer.Coin

			coinArbitrageAmount := validBuyOffer.Price * validSellOffer.Quantity
			if coinArbitrageAmount > maxTradeAmount {
				coinArbitrageAmount = maxTradeAmount
				tradeQuantity := coinArbitrageAmount / validBuyOffer.Price
				tradeQuantity = calculateTradeQuantity(coin, tradeQuantity, validBuyOffer.Exchange, validSellOffer.Exchange)
				validBuyOffer.Quantity = tradeQuantity
				validSellOffer.Quantity = tradeQuantity
			}
			maxTradeAmount -= coinArbitrageAmount

			wg.Add(1)
			go completeRebalanceArbitrageTrade(&wg, apiClient, validBuyOffer, validSellOffer)
		}
		wg.Wait()

		currentSide, maxTradeAmount = determineRebalanceSide(base)
		if currentSide != side || maxTradeAmount < common.MinValidOrderAmount {
			rebalanceRequired = false
			break
		}
		maxRetries -= 1
		time.Sleep(5 * time.Second)
	}

	// TODO(akul): Instead of placing direct order, try to place top offer by changing the price.
	if maxRetries <= 0 && rebalanceRequired && currentSide == side && maxTradeAmount >= common.MinValidOrderAmount {
		common.Log(common.WARNING, "No suitable arbitrage to rebalance USDT - `%s` - %f amount.", currentSide, maxTradeAmount)
		usdtBidAsk, err := utils.GetBidsAndAsks(apiClient, common.BaseUSDT, common.ExcCoinswitch, 1)
		if err != nil {
			return
		}
		var tradeOffer common.CoinMeta
		if side == "buy" {
			tradeOffer = usdtBidAsk.Asks[0]
		} else {
			tradeOffer = usdtBidAsk.Bids[0]
		}
		quantity := maxTradeAmount / tradeOffer.Price
		quantity = utils.ConvertToPreciseQuantity(quantity, common.BaseUSDT, common.ExcCoinswitch)
		tradeOffer.Quantity = quantity

		orderID, err := utils.PlaceOrder(apiClient, tradeOffer, side)
		if err != nil {
			common.Log(common.ERROR, "unable to place MARKET order for USDT rebalancing on %s : %s", tradeOffer.Exchange, side)
			return
		}

		common.Log(common.INFO, "Placed MARKET order for USDT rebalance on %s : %s with price: %.5f/- rate with amount: %.5f.", tradeOffer.Exchange, side, tradeOffer.Price, maxTradeAmount)

		orderData, _ := apiClient.GetOrderById(map[string]interface{}{"order_id": orderID})
		startTime := time.Now()
		isOrderClosed := false
		for !isOrderClosed && time.Since(startTime).Seconds() < 60 {
			time.Sleep(1 * time.Second)
			orderData, _ = apiClient.GetOrderById(map[string]interface{}{"order_id": orderID})
			isOrderClosed = orderData.Status == "EXECUTED"
		}

		if !isOrderClosed {
			common.Log(common.ERROR, "failed to fulfill MARKET order for USDT rebalancing on %s : %s", tradeOffer.Exchange, side)
			orderData = utils.CancelOrderById(apiClient, orderID)
		}
		utils.UpdateAvgBuyPrice(common.BaseUSDT, orderData.Price, orderData.ExecutedQuantity, side == "buy")
	}
}
