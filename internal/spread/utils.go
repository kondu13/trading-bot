package spread

import (
	"math"
	"time"
	"trade_go/internal/api"
	"trade_go/internal/common"
	"trade_go/internal/configs"
	"trade_go/internal/utils"
)

func cancelExistingSpreadBuyOrder(apiClient *api.ApiTradingClient, coin string) {
	// Reads the existing order id from redis, cancel and delete from redis.
	existingBuyOrderId := common.ReadSpreadOrderIdFromRedis(coin)
	if existingBuyOrderId != "" {
		_ = utils.CancelOrderById(apiClient, existingBuyOrderId)
		common.RemoveSpreadOrderIdFromRedis(coin)
	}
}

func calculateProfitPercent(buyOffer, sellOffer common.CoinMeta, intent string) float64 {

	var buyPrice, sellPrice float64

	if intent == "spread1" {
		buyPrice = buyOffer.Price
		sellPrice = sellOffer.PriceWithUsdtBid
	} else if intent == "spread2" {
		buyPrice = buyOffer.PriceWithUsdtAsk
		sellPrice = sellOffer.Price
	} else {
		buyPrice = buyOffer.Price
		sellPrice = sellOffer.Price
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

	return netProfit / spendingAmount
}

func fetchSpread1SellOffer(
	apiClient *api.ApiTradingClient,
	coin string,
	buyOffer common.CoinMeta,
	maxTradeQuantity float64,
) (float64, common.CoinMeta, common.CoinMeta) {
	/*
		Check the market SELL orders on both USDT base exchange.
		Return the sell offer with maximum margin.
	*/
	sellOffer := common.CoinMeta{
		Coin:  coin,
		Price: 0.0,
	}
	buyTradeQuantity := buyOffer.Quantity
	bestProfit := math.Inf(-1)
	for _, exchange := range common.Spread1SellExchanges {
		bidAsk, err := utils.GetBidsAndAsks(apiClient, coin, exchange, 1)
		if err != nil {
			continue
		}
		currentSellOffer := bidAsk.Bids[0]
		tradeQuantity := min(maxTradeQuantity, buyTradeQuantity, currentSellOffer.Quantity)

		buyOffer.Quantity = utils.ConvertToPreciseQuantity(tradeQuantity, coin, buyOffer.Exchange)

		profitPercent := calculateProfitPercent(buyOffer, currentSellOffer, "spread1")
		if profitPercent >= configs.MinSpread1SellProfitPercent && profitPercent > bestProfit {
			bestProfit = profitPercent
			sellOffer = currentSellOffer
			sellOffer.Quantity = utils.ConvertToPreciseQuantity(tradeQuantity, coin, currentSellOffer.Exchange)
		}
	}
	return bestProfit, buyOffer, sellOffer
}

func fetchSpread1BuyOffer(apiClient *api.ApiTradingClient, coin string, orderAmount float64) (float64, common.CoinMeta, common.CoinMeta) {
	if !common.DoesListContainsString(common.Spread1Coins, coin) || common.DoesListContainsString(configs.NotTradedCoins, coin) {
		return 0, common.CoinMeta{}, common.CoinMeta{}
	}

	buyOffers, err := utils.GetBidsAndAsks(apiClient, coin, common.ExcCoinswitch, 1)
	if err != nil {
		return 0, common.CoinMeta{}, common.CoinMeta{}
	}
	buyOffer := buyOffers.Bids[0]

	if buyOffer.Price > 0 {
		inrBalance := utils.ReadCachedCoinBalance(common.BaseINR)
		orderAmount = float64(min(int(orderAmount), int(inrBalance/2)))
		maxTradeQuantity := orderAmount / buyOffer.Price

		profitPercent, buyOffer, sellOffer := fetchSpread1SellOffer(apiClient, coin, buyOffer, maxTradeQuantity)
		return profitPercent, buyOffer, sellOffer
	}
	return 0, common.CoinMeta{}, common.CoinMeta{}
}

func openBuyOrderLoop(apiClient *api.ApiTradingClient, orderID string, offer common.CoinMeta) (bool, common.CoinMeta) {
	/* Keep the limit BUY limit order open on CSX until the following conditions are true.
	1. Latest spread > min_sell_threshold
	2. Latest bid on csx is equal to buy order price.
	*/
	coin := offer.Coin
	for {
		time.Sleep(2 * time.Second)
		orderData, _ := apiClient.GetOrderById(map[string]interface{}{"order_id": orderID})

		if orderData.Status == "EXECUTED" {
			common.RemoveSpreadOrderIdFromRedis(coin)
			// common.Log(common.INFO, "spread-1 BUY order for %s is executed", offer.Coin)
			executedQuantity := orderData.ExecutedQuantity
			if executedQuantity > 0 {
				utils.UpdateAvgBuyPrice(offer.Coin, offer.Price, executedQuantity, true)
				return true, common.CoinMeta{}
			}
		}

		csxBidAsk, err := utils.GetBidsAndAsks(apiClient, offer.Coin, common.ExcCoinswitch, common.MaxDepth)
		if err != nil {
			continue
		}
		liveBuyOffer := csxBidAsk.Bids[0]

		liveOfferAmount := liveBuyOffer.Price * liveBuyOffer.Quantity
		if liveBuyOffer.Price != offer.Price && liveOfferAmount > common.MinValidOrderAmount {

			orderData := utils.CancelOrderById(apiClient, orderID)
			common.RemoveSpreadOrderIdFromRedis(coin)

			executedQuantity := orderData.ExecutedQuantity
			if executedQuantity > 0 {
				utils.UpdateAvgBuyPrice(offer.Coin, offer.Price, executedQuantity, true)
				offer.Quantity = executedQuantity
				return true, offer

			}
			return false, common.CoinMeta{}
		}
	}
}

func placeSpread1BuyOrder(apiClient *api.ApiTradingClient, coin string, orderAmount float64) (bool, common.CoinMeta) {
	/*
		Strategy: BUY on csx "bid" [limit order] -> SELL on c2c1 "bid"
		Steps to follow:
		1. Cancel any existing limit "buy" orders for the coin.
		2. Fetch existing spread and select a buy/sell offer.
		3. Place buy limit order and wait until existing spread reaches min spread.
		4. If buy is successful, place sell order.
	*/
	cancelExistingSpreadBuyOrder(apiClient, coin)

	spreadMargin, buyOffer, sellOffer := fetchSpread1BuyOffer(apiClient, coin, orderAmount)

	// debugging
	// if spreadMargin != math.Inf(-1) {
	// 	common.Log(common.INFO, "%s: spread-1 : found BUY spread: %.4f, BUY on %s and SELL on %s.", coin, spreadMargin, buyOffer.Exchange, sellOffer.Exchange)
	// }

	if spreadMargin >= configs.MinSpread1BuyProfitPercent && sellOffer.Coin != "" && buyOffer.Coin != "" && buyOffer.Quantity*buyOffer.Price > common.MinValidOrderAmount {
		common.Log(common.INFO, "%s: spread-1 : found BUY spread: %.4f, BUY on %s and SELL on %s.", coin, spreadMargin, buyOffer.Exchange, sellOffer.Exchange)

		buyOffer = utils.CreateTopOffer(buyOffer, "increase")

		buyOrderID, _ := utils.PlaceOrder(apiClient, buyOffer, "buy")
		common.AppendSpreadOrderDetailsToRedis(coin, buyOrderID)

		if buyOrderID != "" {
			return openBuyOrderLoop(apiClient, buyOrderID, buyOffer)
		}
	}
	return false, common.CoinMeta{}
}

func placeSpread1SellOrder(apiClient *api.ApiTradingClient, coin string, buyOffer common.CoinMeta) {
	profitPercent, buyOffer, sellOffer := fetchSpread1SellOffer(apiClient, coin, buyOffer, buyOffer.Quantity)

	if sellOffer.Coin != "" && profitPercent >= configs.MinSpread1SellProfitPercent {
		sellOffer = utils.CreateTopOffer(sellOffer, "decrease")

		_, redisQuantity := common.ReadAvgBuyPriceFromRedis(coin)
		sellOfferQuantity := sellOffer.Quantity

		if redisQuantity < sellOfferQuantity {
			sellOfferQuantity = utils.ConvertToPreciseQuantity(redisQuantity, coin, sellOffer.Exchange)
			sellOffer.Quantity = sellOfferQuantity
		}

		sellOrderID, err := utils.PlaceOrder(apiClient, sellOffer, "sell")
		if err == nil && sellOrderID != "" {
			common.AppendOrderDetailsToRedis(sellOffer.Coin, sellOrderID, buyOffer.Price)
			utils.UpdateAvgBuyPrice(sellOffer.Coin, buyOffer.Price, sellOffer.Quantity, false)
		}
	}
}

func fetchSpread2BuyOffer(apiClient *api.ApiTradingClient, coin string, orderAmount float64) (float64, common.CoinMeta, common.CoinMeta) {
	if !common.DoesListContainsString(configs.Spread2Coins, coin) || common.DoesListContainsString(configs.NotTradedCoins, coin) {
		return 0, common.CoinMeta{}, common.CoinMeta{}
	}

	buyOffers, err := utils.GetBidsAndAsks(apiClient, coin, common.ExcBinance, 1)
	if err != nil {
		return 0, common.CoinMeta{}, common.CoinMeta{}
	}
	buyOffer := buyOffers.Asks[0]

	sellOffers, err := utils.GetBidsAndAsks(apiClient, coin, common.ExcCoinswitch, 1)
	if err != nil {
		return 0, common.CoinMeta{}, common.CoinMeta{}
	}
	sellOffer := sellOffers.Asks[0]

	if buyOffer.Coin != "" && sellOffer.Coin != "" {

		usdtBalance := utils.ReadCachedCoinBalance(common.BaseUSDT)
		orderAmount = float64(min(int(orderAmount), int(usdtBalance/2)))
		maxTradeQuantity := orderAmount / buyOffer.Price

		tradeQuantity := min(maxTradeQuantity, buyOffer.Quantity, sellOffer.Quantity)

		buyOffer.Quantity = utils.ConvertToPreciseQuantity(tradeQuantity, coin, buyOffer.Exchange)
		sellOffer.Quantity = buyOffer.Quantity

		profitPercent := calculateProfitPercent(buyOffer, sellOffer, "spread2")

		return profitPercent, buyOffer, sellOffer
	}
	return 0, common.CoinMeta{}, common.CoinMeta{}
}

func fetchSpread2SellOffer(apiClient *api.ApiTradingClient, coin string, buyOffer common.CoinMeta) common.CoinMeta {
	sellOffers, err := utils.GetBidsAndAsks(apiClient, coin, common.ExcCoinswitch, 1)
	if err != nil {
		return common.CoinMeta{}
	}

	sellOffer := sellOffers.Asks[0]
	if sellOffer.Price == 0 {
		return common.CoinMeta{}
	}
	tradeQuantity := buyOffer.Quantity
	sellOffer.Quantity = utils.ConvertToPreciseQuantity(tradeQuantity, coin, sellOffer.Exchange)

	profitPercent := calculateProfitPercent(buyOffer, sellOffer, "spread2")
	if profitPercent >= configs.MinSpread2SellProfitPercent {
		common.Log(common.INFO, "%s -> spread-2: %.4f -> SELL: %s: price: %.4f -> quantity: %.5f", coin, profitPercent, sellOffer.Exchange, sellOffer.PriceWithUsdtBid, sellOffer.Quantity)
		return sellOffer
	}
	return common.CoinMeta{}
}

func placeSpread2BuyOrder(apiClient *api.ApiTradingClient, coin string, orderAmount float64) {
	/*
		Strategy: BUY on c2c1 "bid" [market order] -> SELL on csx "ask" [limit order]
		Steps to follow:
		1. Fetch existing spread and select a buy/sell offer.
		2. Place buy market order and place a limit sell offer.
	*/
	spreadMargin, buyOffer, sellOffer := fetchSpread2BuyOffer(apiClient, coin, orderAmount)
	if spreadMargin < configs.MinSpread2BuyProfitPercent || sellOffer.Coin == "" || buyOffer.Coin == "" || common.IsValidOfferByOrderAmount(&buyOffer) {
		return
	}
	common.Log(common.INFO, "%s: spread-2 : found BUY spread: %.4f, BUY on %s and SELL on %s.", coin, spreadMargin, buyOffer.Exchange, sellOffer.Exchange)

	buyOffer = utils.CreateTopOffer(buyOffer, "increase")

	buyOrderID, _ := utils.PlaceOrder(apiClient, buyOffer, "buy")

	if buyOrderID != "" {
		stTime := time.Now()
		timeSpent := 0.0
		isOrderClosed := false
		for !isOrderClosed && timeSpent <= 60 {
			time.Sleep(1 * time.Second)
			isOrderClosed, _ = utils.IsOrderClosed(apiClient, buyOrderID)
			timeSpent = time.Since(stTime).Seconds()
		}
		var orderData common.CSXOrder
		if !isOrderClosed {
			orderData = utils.CancelOrderById(apiClient, buyOrderID)
		}
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

		executedQuantity := orderData.ExecutedQuantity
		buyOffer.Quantity = executedQuantity

		if executedQuantity > 0 {
			utils.UpdateAvgBuyPrice(buyOffer.Coin, buyOffer.Price, executedQuantity, true)

			sellOffer = fetchSpread2SellOffer(apiClient, coin, buyOffer)
			if sellOffer.Coin != "" && common.IsValidOfferByOrderAmount(&sellOffer) {
				sellOrderID, err := utils.PlaceOrder(apiClient, sellOffer, "sell")
				if err == nil && sellOrderID != "" {
					common.AppendOrderDetailsToRedis(sellOffer.Coin, sellOrderID, buyOffer.Price)
					utils.UpdateAvgBuyPrice(sellOffer.Coin, buyOffer.Price, sellOffer.Quantity, false)
				}
			}
		}
	}
}
