package main

import (
	"time"
	"trade_bot/internal/api"
	"trade_bot/internal/common"
	"trade_bot/internal/utils"
)

func TestTDSPercent(apiClient *api.ApiTradingClient) {
	buyOffers, err := utils.GetBidsAndAsks(apiClient, "BTC", common.ExcBinance, 1)
	if err == nil {
		buyOffer := buyOffers.Asks[0]
		if common.IsValidOfferByOrderAmount(&buyOffer) {
			buyOffer.Quantity = common.MinValidOrderAmount / buyOffer.Price
			buyOffer.Quantity = utils.ConvertToPreciseQuantity(buyOffer.Quantity, "BTC", common.ExcBinance)
			usdtQuantityRequired1 := (buyOffer.PriceInUsdt) * (buyOffer.Quantity)
			avg1, qbtc1 := common.ReadAvgBuyPriceFromRedis("BTC")
			portfolio1, err := apiClient.GetUserPortfolio()
			btcPortfolio1 := -1.0
			usdtQuantity1 := -1.0
			if err == nil {
				btcPortfolio1 = portfolio1["BTC"].Quantity
				usdtQuantity1 = portfolio1[common.BaseUSDT].Quantity
			} else {
				common.Log(common.WARNING, "RETURNING.......")
				return
			}

			common.Log(common.INFO, "valid offer found in binance,buyprice:%f , buyquantity:%f", buyOffer.PriceInUsdt, buyOffer.Quantity)
			common.Log(common.INFO, "USDT Balance: %f, USDT Required: %f", usdtQuantity1, usdtQuantityRequired1)
			common.Log(common.INFO, "btc avgPrice: %f, quantity:%f, quantity in portfolio: %f", avg1, qbtc1, btcPortfolio1)

			buyOrderID, err := utils.PlaceOrder(apiClient, buyOffer, "buy")
			if buyOrderID != "" && err == nil {
				var isOrderClosed bool
				var topBuyPrice float64
				var executedQuantity float64
				var bidAskBuyExchange common.BidAskDepth
				startTime := time.Now()
				for {
					isOrderClosed, _ = utils.IsOrderClosed(apiClient, buyOrderID)
					bidAskBuyExchange, _ = utils.GetBidsAndAsks(apiClient, buyOffer.Coin, buyOffer.Exchange, 1)
					topBuyPrice = bidAskBuyExchange.Asks[0].Price
					if (isOrderClosed) || (topBuyPrice < buyOffer.Price) || (time.Since(startTime).Seconds() > common.MaxWaitingTime) {
						break
					}
				}
				orderData, _ := apiClient.GetOrderById(map[string]interface{}{"order_id": buyOrderID})
				isOrderClosed = orderData.Status == "EXECUTED"
				if !isOrderClosed {
					orderData = utils.CancelOrderById(apiClient, buyOrderID)
				}
				executedQuantity = orderData.ExecutedQuantity
				if executedQuantity > 0 {
					common.Log(common.INFO, "partial order for %s executed: %f/%f quantity", orderData.Symbol, executedQuantity, orderData.OriginalQuantity)
					utils.UpdateAvgBuyPrice(buyOffer.Coin, buyOffer.Price, executedQuantity, true)
				}
				btcBought := executedQuantity
				usdtSpent := btcBought * buyOffer.PriceInUsdt
				avg, qbtc := common.ReadAvgBuyPriceFromRedis("BTC")
				portfolio, err := apiClient.GetUserPortfolio()
				btcPortfolio := -1.0
				usdtQuantity := -1.0
				if err == nil {
					btcPortfolio = portfolio["BTC"].Quantity
					usdtQuantity = portfolio[common.BaseUSDT].Quantity
				}
				expectedAvg := (avg1*qbtc1 + buyOffer.Price*executedQuantity) / (executedQuantity + qbtc1)
				tdsAmount := 0.010101 * usdtSpent
				common.Log(common.INFO, "USDT Balance: %f", usdtQuantity)
				common.Log(common.INFO, "btc ExpectedavgPrice: %f, Expectedquantity:%f, quantity in portfolio: %f", expectedAvg, (executedQuantity + qbtc1), btcPortfolio)
				common.Log(common.INFO, "btc avgPrice: %f, quantity:%f, quantity in portfolio: %f", avg, qbtc, btcPortfolio)
				common.Log(common.INFO, "Expected balance: %f, portfolio balance:%f", usdtQuantity1-usdtSpent-tdsAmount, usdtQuantity)
				common.Log(common.INFO, "btcBought: %f, porfolio difference before and after buying: %f", btcBought, btcPortfolio-btcPortfolio1)
				common.Log(common.INFO, "usdtSpent:%f, usdtQuantityDifferenceInPortfolio: %f", usdtSpent, usdtQuantity1-usdtQuantity)
				common.Log(common.INFO, "expected tds amount:%f,actual tds amount: %f", tdsAmount, usdtQuantity1-usdtQuantity-usdtSpent)
				common.Log(common.INFO, "actual tds percent: %f", ((usdtQuantity1-usdtQuantity)/usdtSpent)-1)
			}

		}
	} else {
		common.Log(common.INFO, "Error in getting latest bidask values from websocket: %v", err)
	}
	sellOffers, err := utils.GetBidsAndAsks(apiClient, "BTC", common.ExcBinance, 1)
	if err == nil {
		sellOffer := sellOffers.Bids[0]
		if common.IsValidOfferByOrderAmount(&sellOffer) {
			portfolio1, err := apiClient.GetUserPortfolio()
			btcPortfolio1 := -1.0
			usdtQuantity1 := -1.0
			if err == nil {
				btcPortfolio1 = portfolio1["BTC"].Quantity
				usdtQuantity1 = portfolio1[common.BaseUSDT].Quantity
			} else {
				common.Log(common.WARNING, "RETURNING.......")
				return
			}
			sellOffer.Quantity = btcPortfolio1
			sellOffer.Quantity = utils.ConvertToPreciseQuantity(sellOffer.Quantity, "BTC", common.ExcBinance)
			avg1, qbtc1 := common.ReadAvgBuyPriceFromRedis("BTC")
			common.Log(common.INFO, "valid offer found in binance,sellPrice:%f , sellquantity:%f", sellOffer.PriceInUsdt, sellOffer.Quantity)
			common.Log(common.INFO, "USDT Balance: %f", usdtQuantity1)
			common.Log(common.INFO, "btc avgPrice: %f, quantity:%f, quantity in portfolio: %f", avg1, qbtc1, btcPortfolio1)
			utils.LogTDSInfo(apiClient)
			sellOrderID, err := utils.PlaceOrder(apiClient, sellOffer, "sell")
			if sellOrderID != "" && err == nil {
				var isOrderClosed bool
				var topSellPrice float64
				var executedQuantity float64
				var bidAskBuyExchange common.BidAskDepth
				startTime := time.Now()
				for {
					isOrderClosed, _ = utils.IsOrderClosed(apiClient, sellOrderID)
					bidAskBuyExchange, _ = utils.GetBidsAndAsks(apiClient, sellOffer.Coin, sellOffer.Exchange, 1)
					topSellPrice = bidAskBuyExchange.Bids[0].Price
					if (isOrderClosed) || (topSellPrice < sellOffer.Price) || (time.Since(startTime).Seconds() > common.MaxWaitingTime) {
						break
					}
				}
				orderData, _ := apiClient.GetOrderById(map[string]interface{}{"order_id": sellOrderID})
				isOrderClosed = orderData.Status == "EXECUTED"
				if !isOrderClosed {
					orderData = utils.CancelOrderById(apiClient, sellOrderID)
				}
				executedQuantity = orderData.ExecutedQuantity
				if executedQuantity > 0 {
					common.Log(common.INFO, "partial order for %s executed: %f/%f quantity", orderData.Symbol, executedQuantity, orderData.OriginalQuantity)
					utils.UpdateAvgBuyPrice(sellOffer.Coin, sellOffer.Price, executedQuantity, false)
				}
				btcBought := executedQuantity
				usdtGain := btcBought * sellOffer.PriceInUsdt
				avg, qbtc := common.ReadAvgBuyPriceFromRedis("BTC")
				portfolio, err := apiClient.GetUserPortfolio()
				btcPortfolio := -1.0
				usdtQuantity := -1.0
				if err == nil {
					btcPortfolio = portfolio["BTC"].Quantity
					usdtQuantity = portfolio[common.BaseUSDT].Quantity
				}
				tdsAmount := common.TDSDetectionPercent * usdtGain
				common.Log(common.INFO, "USDT Balance: %f", usdtQuantity)
				// common.Log(common.INFO, "btc ExpectedavgPrice: %f, Expectedquantity:%f, quantity in portfolio: %f", expectedAvg, (executedQuantity+qbtc1), btcPortfolio)
				common.Log(common.INFO, "btc avgPrice: %f, quantity:%f, quantity in portfolio: %f", avg, qbtc, btcPortfolio)
				common.Log(common.INFO, "btcSold: %f, porfolio difference before and after selling: %f", btcBought, btcPortfolio-btcPortfolio1)
				common.Log(common.INFO, "usdtGain:%f, usdtQuantityDifferenceInPortfolio: %f", usdtGain, usdtQuantity-usdtQuantity1)
				common.Log(common.INFO, "Expected balance: %f, portfolio balance:%f", usdtQuantity1+usdtGain-tdsAmount, usdtQuantity)
				common.Log(common.INFO, "expected tds amount:%f,actual tds amount: %f", tdsAmount, usdtGain-usdtQuantity+usdtQuantity1)
				common.Log(common.INFO, "actual tds percent: %f", 1-((usdtQuantity-usdtQuantity1)/usdtGain))
			}

		}
	} else {
		common.Log(common.INFO, "Error in getting latest bidask values from websocket: %v", err)
	}
}
