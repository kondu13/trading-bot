package utils

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"trade_go/internal/api"
	"trade_go/internal/common"
	"trade_go/internal/configs"
)

func PlaceOrder(apiClient *api.ApiTradingClient, order common.CoinMeta, side string) (string, error) {
	// Start time measurement
	stTime := time.Now()

	// Validate side
	if side != "sell" && side != "buy" {
		return "", fmt.Errorf("invalid order side: `%s`", side)
	}
	logSide := strings.ToUpper(side)
	orderAmount := order.Price * order.Quantity

	// Prepare symbol and price
	symbol := fmt.Sprintf("%s/%s", order.Coin, order.Base)
	price := order.Price
	if common.IsUSDTBaseExchange(order.Exchange) {
		symbol = fmt.Sprintf("%s/%s", order.Coin, common.BaseUSDT)
		//akul(TODO): Please note that this price is updated if the offer amount is updated.
		price = order.PriceInUsdt
	}

	// Create payload
	payload := map[string]interface{}{
		"side":     side,
		"symbol":   symbol,
		"type":     "limit",
		"price":    price,
		"quantity": order.Quantity,
		"exchange": order.Exchange,
	}

	// Send order request
	response, err := apiClient.CreateOrder(payload)
	if err != nil {
		return "", fmt.Errorf("%s order for %s : %s -> error creating order: %w", logSide, symbol, order.Exchange, err)
	}

	// Check error message.
	responseMessage, exists := response["message"]
	if exists {
		common.Log(common.ERROR, "%s order for %s : %s -> creating order: %v -> payload: %v", logSide, symbol, order.Exchange, responseMessage, payload)
		return "", fmt.Errorf("%s order for %s : %s -> error creating order: %v -> payload: %v", logSide, symbol, order.Exchange, responseMessage, payload)
	}

	// Check response
	data, ok := response["data"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("%s order for %s : %s -> response data not found or invalid", logSide, symbol, order.Exchange)
	}

	orderID, ok := data["order_id"].(string)
	if !ok {
		return "", fmt.Errorf("%s order for %s : %s -> order_id not found or invalid", logSide, symbol, order.Exchange)
	}

	// Log success
	duration := time.Since(stTime).Seconds()
	common.Log(common.INFO,
		"%s placed for %s at price : %f on %s quantity: %f amount: %.3f in %.3f/- secs.",
		logSide, order.Coin, price, order.Exchange, order.Quantity, orderAmount, duration,
	)
	return orderID, nil
}

func IsOrderClosed(apiClient *api.ApiTradingClient, orderID string) (bool, float64) {
	params := map[string]interface{}{"order_id": orderID}
	csxOrder, err := apiClient.GetOrderById(params)
	if err != nil {
		common.Log(common.ERROR, "fetching order id %v: %v", orderID, err)
		return false, 0
	}
	if csxOrder.Status == "EXECUTED" {
		return true, csxOrder.ExecutedQuantity
	}
	return false, 0
}

func UpdateAvgBuyPrice(coin string, buyPrice float64, quantity float64, toAdd bool) {
	avgBuyPrice, totalQuantity := common.ReadAvgBuyPriceFromRedis(coin)
	// common.Log(common.INFO, "BEFORE: %s: AvgBuyPrice: %.5f -> totalQuantity: %.5f", coin, avgBuyPrice, totalQuantity)
	// common.Log(common.INFO, "%s: toAdd: %v buyPrice: %v -> quantity: %v", coin, toAdd, buyPrice, quantity)
	if quantity == 0.0 || buyPrice == 0.0 {
		return
	}
	if toAdd {
		avgBuyPrice = (avgBuyPrice*totalQuantity + quantity*buyPrice) / (totalQuantity + quantity)
		totalQuantity += quantity
	} else {
		if quantity == totalQuantity {
			common.SaveAvgBuyPriceToRedis(coin, 0, 0)
			// common.Log(common.INFO, "AFTER: %s: AvgBuyPrice: 0 -> totalQuantity: 0", coin)
			return
		}
		avgBuyPrice = (avgBuyPrice*totalQuantity - quantity*buyPrice) / (totalQuantity - quantity)
		totalQuantity -= quantity
	}
	common.SaveAvgBuyPriceToRedis(coin, avgBuyPrice, totalQuantity)
	// common.Log(common.INFO, "AFTER: %s: AvgBuyPrice: %.5f -> totalQuantity: %.5f", coin, avgBuyPrice, totalQuantity)
}

func fetchBestSellOffer(
	apiClient *api.ApiTradingClient,
	side, coin string,
	avgBuyPrice, maxSellQuantity float64,
) common.CoinMeta {
	sellOffer := common.CoinMeta{
		Coin:  coin,
		Price: 0.0,
	}
	if side != "ask" && side != "bid" {
		common.Log(common.ERROR, "Invalid side for finding best sell offer: %s", side)
		return sellOffer
	}
	bestProfit := math.Inf(-1)
	bestProfitMargin := math.Inf(-1)

	common.ReadWriteMutex.RLock()
	allExchangeCommissions := common.CommissionFee
	common.ReadWriteMutex.RUnlock()

	maxCommissionFee := 0.0
	for _, exchangeCommission := range allExchangeCommissions {
		maxCommissionFee = max(maxCommissionFee, exchangeCommission)
	}
	// Always use market price to sell if exchange is binance or kucoin.
	exchanges := []string{common.ExcCoinswitch}
	if side == "bid" {
		exchanges = common.AllExchanges
	}
	var isViableTrade bool
	for _, exchange := range exchanges {
		bidAsk, _ := GetBidsAndAsks(apiClient, coin, exchange, 1)

		currentOffer := bidAsk.Bids[0]
		if side == "ask" {
			currentOffer = bidAsk.Asks[0]
		}
		if currentOffer.Price == 0.0 || currentOffer.Quantity == 0.0 {
			continue
		}
		tradeQuantity := ConvertToPreciseQuantity(maxSellQuantity, coin, currentOffer.Exchange)
		currentOffer.Quantity = tradeQuantity
		if !common.IsValidOfferByOrderAmount(&currentOffer) {
			continue
		}
		profit := (currentOffer.Price - avgBuyPrice) * maxSellQuantity
		profitMargin := (currentOffer.Price - avgBuyPrice) / avgBuyPrice

		sellCommissionFee := allExchangeCommissions[currentOffer.Exchange]

		totalCommissionAmount := (sellCommissionFee*currentOffer.Price + maxCommissionFee*avgBuyPrice) * maxSellQuantity
		netProfit := profit - totalCommissionAmount

		// TODO(akul): Verify whether to use buyPrice or sellPrice here.
		tradeAmount := avgBuyPrice*maxSellQuantity + totalCommissionAmount

		minProfitPercentage := configs.MinPortfolioSellProfitPercent
		if common.DoesListContainsString(common.Spread1Coins, coin) {
			minProfitPercentage = configs.MinSpread1SellProfitPercent
		} else if common.DoesListContainsString(configs.Spread2Coins, coin) {
			minProfitPercentage = configs.MinSpread2SellProfitPercent
		}

		isUSDTBaseExchange := common.IsUSDTBaseExchange(currentOffer.Exchange)
		// minProfit := min(common.SellPortfolioMinProfit, configs.MinPortfolioSellProfitPercent*(avgBuyPrice*maxSellQuantity+totalCommissionAmount))

		// if profit < minProfit {
		// 	continue
		// }

		if bestProfit <= netProfit {
			if bestProfit == netProfit && isUSDTBaseExchange {
				continue
			}
			bestProfit = netProfit
			sellOffer = currentOffer
			bestProfitMargin = profitMargin
			isViableTrade = (netProfit / tradeAmount) >= minProfitPercentage
			// common.Log(common.INFO, "found bestSellOffer %s:%s:%s -> margin: %.3f", sellOffer.Coin, sellOffer.Exchange, side, bestProfitMargin)
		}
	}
	// Return a market order if coin hits the stop loss.
	if side == "bid" &&
		bestProfitMargin > math.Inf(-1) &&
		bestProfitMargin <= configs.SellProfitStopLoss &&
		!common.DoesListContainsString(configs.NotSellAtStopLoss, coin) &&
		!common.DoesListContainsString(common.Spread1Coins, coin) &&
		!common.DoesListContainsString(configs.Spread2Coins, coin) {

		common.Log(common.INFO, "found bestSellOffer at STOP loss: %s:%s:%s -> margin: %.3f", sellOffer.Coin, sellOffer.Exchange, side, bestProfitMargin)
		return sellOffer
	}

	if isViableTrade && bestProfit >= common.SellPortfolioMinProfit {
		common.Log(common.INFO, "found bestSellOffer : %s:%s:%s -> margin: %.3f", sellOffer.Coin, sellOffer.Exchange, side, bestProfitMargin)
		return sellOffer
	}
	return common.CoinMeta{
		Coin:  coin,
		Price: 0.0,
	}
}

func CreateTopOffer(offer common.CoinMeta, side string) common.CoinMeta {
	if side != "increase" && side != "decrease" {
		common.Log(common.ERROR, "Invalid side for updating top price: %s", side)
		return offer
	}
	common.ReadWriteMutex.RLock()
	allExchangePrecisions := common.AllExchangeCoinPrecisions
	common.ReadWriteMutex.RUnlock()
	precisions := allExchangePrecisions[offer.Exchange][offer.Coin]

	price := offer.Price

	isUSDTExchange := common.IsUSDTBaseExchange(offer.Exchange)

	// Convert INR base price to USDT for correct calculation.
	if isUSDTExchange && offer.Base == common.BaseINR {
		price = offer.PriceInUsdt
		// common.ReadWriteMutex.RLock()
		// usdtMarketPrices := common.UsdtBidAskPrice
		// common.ReadWriteMutex.RUnlock()
		// price /= usdtMarketPrices["average"]
	}

	switch side {
	case "increase":
		if isUSDTExchange {
			price *= 1.01
		} else {
			price += math.Pow(10, -float64(precisions["limit"]))
		}
	case "decrease":
		if isUSDTExchange {
			price *= 0.99
		} else {
			price -= math.Pow(10, -float64(precisions["limit"]))
		}
	}

	// Convert the updated price into the required exchange precision.
	power := math.Pow(10, float64(precisions["limit"]))
	price = math.Trunc(price*power) / power

	// Convert back USDT base price to INR for saving the price in coinMeta.
	if common.IsUSDTBaseExchange(offer.Exchange) && offer.Base == common.BaseINR {
		offer.PriceInUsdt = price
		price = ConvertUsdtToInr(price, "average")
	}

	offer.Price = price

	return offer
}

func placePortfolioSellOrder(apiClient *api.ApiTradingClient) {
	common.ReadWriteMutex.RLock()
	portfolioBalance := common.AccountPortfolioBalance
	common.ReadWriteMutex.RUnlock()
	for coin, balance := range portfolioBalance {
		if common.DoesListContainsString(configs.NotTradedCoins, coin) {
			continue
		}
		if coin == common.BaseINR || coin == common.BaseUSDT {
			continue
		}
		if balance.Amount < common.MinValidOrderAmountInrBase {
			continue
		}
		avgBuyPrice, totalQuantity := common.ReadAvgBuyPriceFromRedis(coin)
		// common.Log(common.INFO, "%s: buyAveragePrice: %.3f : portfolioQuantity: %.3f", coin, avgBuyPrice, totalQuantity)
		/* Fix the redis average buy price and quantity.
		TODO(akul): Make sure the redis values always match with the portfolio balance.
		*/
		// if math.IsNaN(totalQuantity) || math.IsNaN(avgBuyPrice) {
		// 	common.Log(common.WARNING, "%s : average buy price: %f and quantity: %f", coin, avgBuyPrice, totalQuantity)
		// 	common.SaveAvgBuyPriceToRedis(coin, 0, 0)
		// 	avgBuyPrice = 0
		// 	totalQuantity = 0
		// }
		// if totalQuantity <= 0 || avgBuyPrice <= 0 {
		// 	livePortfolioDetail, err := apiClient.GetUserPortfolio()
		// 	if err != nil {
		// 		continue
		// 	}
		// 	coinPortfolioDetail, ok := livePortfolioDetail[coin]
		// 	if !ok {
		// 		continue
		// 	}
		// 	if coinPortfolioDetail.Amount < common.MinValidOrderAmount {
		// 		continue
		// 	}
		// 	avgBuyPrice = coinPortfolioDetail.Amount / coinPortfolioDetail.Quantity
		// 	totalQuantity = coinPortfolioDetail.Quantity

		// 	if totalQuantity > 0 {
		// 		common.Log(common.WARNING, "updated buy avg price for %s in redis from API.", coin)
		// 		UpdateAvgBuyPrice(coin, avgBuyPrice, totalQuantity, true)
		// 	}

		// }
		if totalQuantity*avgBuyPrice < common.MinValidOrderAmountInrBase {
			continue
		}
		// common.Log(common.INFO, "attempting to sell portfolio balance of %f for %s", totalQuantity*avgBuyPrice, coin)
		sellOffer := fetchBestSellOffer(apiClient, "bid", coin, avgBuyPrice, totalQuantity)
		if sellOffer.Price != 0.0 {
			sellOffer = CreateTopOffer(sellOffer, "decrease")
			sellOrderID, err := PlaceOrder(apiClient, sellOffer, "sell")
			if err == nil && sellOrderID != "" {
				common.AppendOrderDetailsToRedis(coin, sellOrderID, avgBuyPrice)
				UpdateAvgBuyPrice(coin, avgBuyPrice, sellOffer.Quantity, false)
				continue
			}
		}
		sellOffer = fetchBestSellOffer(apiClient, "ask", coin, avgBuyPrice, totalQuantity)
		if sellOffer.Price != 0.0 {
			sellOffer = CreateTopOffer(sellOffer, "decrease")
			sellOrderID, err := PlaceOrder(apiClient, sellOffer, "sell")
			if err == nil && sellOrderID != "" {
				common.AppendOrderDetailsToRedis(coin, sellOrderID, avgBuyPrice)
				UpdateAvgBuyPrice(coin, avgBuyPrice, sellOffer.Quantity, false)
			}
		}
	}
}

func updateUSDTAvgBuyPriceForClosedSellOrder(orderData common.CSXOrder) {
	// Updates the redis price and quantity when a sell order is closed on a USDT base exchange.
	if orderData.ExecutedQuantity > 0 {
		common.ReadWriteMutex.RLock()
		sellCommissionFee := common.CommissionFee
		usdtBidAskPrice := common.UsdtBidAskPrice
		common.ReadWriteMutex.RUnlock()

		usdtGain := orderData.Price * orderData.ExecutedQuantity
		usdtCommissionAmount := orderData.Price * orderData.ExecutedQuantity * sellCommissionFee[orderData.Exchange]
		usdtTDSAmount := orderData.Price * orderData.ExecutedQuantity * (common.TDSDetectionPercent)

		usdtGain = usdtGain - usdtCommissionAmount - usdtTDSAmount

		//TODO(akul): Confirm in testing if this logic is working or not.
		// inrAmountSpent := buyPrice * orderData.ExecutedQuantity
		// averageUsdtPrice := inrAmountSpent / usdtGain
		// usdtBuyPrice := max(averageUsdtPrice, usdtBidAskPrice["average"])

		UpdateAvgBuyPrice(common.BaseUSDT, usdtBidAskPrice["ask"], usdtGain, true)
	}
}

func closeAndupdateRedisForPartialSellOrder(apiClient *api.ApiTradingClient, coin string, buyPrice float64, sellOrderId string) {
	orderData := CancelOrderById(apiClient, sellOrderId)
	common.RemoveOrderIDFromRedis(coin, sellOrderId)
	if common.IsUSDTBaseExchange(orderData.Exchange) {
		updateUSDTAvgBuyPriceForClosedSellOrder(orderData)
	}
	if orderData.RemainingQuantity > 0 {
		UpdateAvgBuyPrice(coin, buyPrice, orderData.RemainingQuantity, true)
	}

}

func processOpenSellOrdersForSingleCoin(wg *sync.WaitGroup, apiClient *api.ApiTradingClient, sellOrders map[string]float64, coin string) {
	/* Checks all the open SELL orders for a specific coin and performs the following actions:
	- If status == "EXECUTED" -> remove the sell order id entry from redis.
	- If status != "EXECUTED" -> Check if sell price is at top of bid or ask for the same exchange.
		- If yes, do nothing.
		- If no, cancel order and update average buy price and quantity for the coin in redis.
	*/
	defer wg.Done()
	for sellOrderID, buyPrice := range sellOrders {
		time.Sleep(500 * time.Microsecond)
		orderData, err := apiClient.GetOrderById(map[string]interface{}{"order_id": sellOrderID})
		if err != nil {
			common.Log(common.ERROR, "fetching order id %v: %v", sellOrderID, err)
			continue
		}
		if orderData.Status == "EXECUTED" || orderData.Status == "CANCELLED" {
			if common.IsUSDTBaseExchange(orderData.Exchange) {
				updateUSDTAvgBuyPriceForClosedSellOrder(orderData)
			}
			common.RemoveOrderIDFromRedis(coin, sellOrderID)
			continue
		}
		/*
			1. Find if there is a bid available with min profit for the coin.
			2. Cancel order if it is not on top of csx ask.
			3. Cancel order if it is placed on other exchange and
		*/
		isOrderOnUSDTExchange := common.IsUSDTBaseExchange(orderData.Exchange)

		// Cancel stale sell order on USDT based exchange.
		if isOrderOnUSDTExchange && time.Since(orderData.CreatedTime).Seconds() >= (2*60) {
			closeAndupdateRedisForPartialSellOrder(apiClient, coin, buyPrice, sellOrderID)
			common.Log(common.WARNING, "%s:%s - cancelled and removed stale sell order on usdt base exchange", coin, orderData.Exchange)
			continue
		} else if isOrderOnUSDTExchange {
			// Wait for the USDT based market sell order to get fulfilled.
			continue
		} else if orderData.Exchange == common.ExcCoinswitch {
			sellOffer := fetchBestSellOffer(apiClient, "bid", coin, buyPrice, orderData.RemainingQuantity)
			// Cancel if there is a better bid offer with min profit margin.
			if sellOffer.Price > 0 && sellOffer.Price != orderData.Price {
				common.Log(common.WARNING, "%s:%s : cancelled sell order since there is a better bid offer available", coin, orderData.Exchange)
				closeAndupdateRedisForPartialSellOrder(apiClient, coin, buyPrice, sellOrderID)
			} else {
				// Check if order on csx is at top ask or not.
				bidAsk, _ := GetBidsAndAsks(apiClient, coin, orderData.Exchange, 1)
				if bidAsk.Bids[0].Price == 0 || bidAsk.Asks[0].Price == 0 {
					common.Log(common.WARNING, "bidAsk price is zero in %s for the coin %s for processing open sell orders", orderData.Exchange, coin)
					return
				}
				if orderData.Price < bidAsk.Bids[0].Price || orderData.Price > bidAsk.Asks[0].Price {
					closeAndupdateRedisForPartialSellOrder(apiClient, coin, buyPrice, sellOrderID)
				}
			}
		} else {
			common.Log(common.WARNING, "%s:%s: skipping open sell order evaluation with order id: %s : time spent since order created: %v seconds", coin, orderData.Exchange, sellOrderID, time.Since(orderData.CreatedTime).Seconds())
		}
	}
}

func processOpenOrders(apiClient *api.ApiTradingClient) {
	common.ReadWriteMutex.RLock()
	commonCoins := common.CommonArbitrageCoins
	common.ReadWriteMutex.RUnlock()
	var wg sync.WaitGroup
	for _, coin := range commonCoins {
		sellOrders := common.ReadOrderDetailsFromRedis(coin)
		if len(sellOrders) > 0 {
			wg.Add(1)
			go processOpenSellOrdersForSingleCoin(&wg, apiClient, sellOrders, coin)
		}
	}
	wg.Wait()
}

func calculateAvgBuyPriceUsingLIFO(apiClient *api.ApiTradingClient, coin string, defaultBuyPrice, quantity float64) float64 {
	/*
		1. Read all "EXECUTED", "BUY" orders for the coin.
		2. Based on descending order of the UpdatedTime.
	*/
	closedOrders, err := apiClient.GetClosedOrders(map[string]interface{}{
		"side":    "buy",
		"symbols": fmt.Sprintf("%s/%s,%s/%s", coin, common.BaseINR, coin, common.BaseUSDT),
		"status":  "EXECUTED",
		"count":   30,
	})
	if err != nil {
		common.Log(common.ERROR, "reading closed buy orders for %s", coin)
	}
	sort.Slice(closedOrders, func(i, j int) bool {
		return closedOrders[i].UpdatedTime.After(closedOrders[j].UpdatedTime)
	})

	var usefulQuantity, totalCost, totalQuantity float64
	for _, order := range closedOrders {
		if quantity <= 0 {
			break
		}
		executedQuantity := order.ExecutedQuantity
		usefulQuantity = 0
		if executedQuantity > 0 {
			if executedQuantity <= quantity {
				usefulQuantity = executedQuantity
			} else if executedQuantity > quantity {
				usefulQuantity = quantity
			}
			if usefulQuantity > 0 {
				quantity -= usefulQuantity
				buyPrice := order.Price
				if common.IsUSDTBaseExchange(order.Exchange) {
					buyPrice = ConvertUsdtToInr(buyPrice, "average")
				}
				totalCost += usefulQuantity * buyPrice
				totalQuantity += usefulQuantity
			}
		}
	}

	// Add remainig quantity amount from the average buy price from API.
	if quantity > 0 {
		common.Log(common.WARNING, "%s: used buyAveragePrice: %f for quantity: %f from API when calculating using LIFO", coin, defaultBuyPrice, quantity)
		totalCost += quantity * defaultBuyPrice
		totalQuantity += quantity
	}

	var buyAvgPrice float64
	if totalCost > 0 && totalQuantity > 0 {
		buyAvgPrice = totalCost / totalQuantity
	}
	return buyAvgPrice
}

func CancelOrderById(apiClient *api.ApiTradingClient, orderId string) common.CSXOrder {
	// Cancels the order and wait for the order status to get finalized.
	_, _ = apiClient.CancelOrder(map[string]interface{}{"order_id": orderId})
	time.Sleep(1 * time.Second)

	orderData, _ := apiClient.GetOrderById(map[string]interface{}{"order_id": orderId})
	if orderData.Price == 0 {
		return orderData
	}
	common.Log(common.WARNING, "cancelled %s order for %s on %s since it was not on top.", orderData.Side, orderData.Symbol, orderData.Exchange)

	// attempt := 0
	for orderData.Status != "EXECUTED" && orderData.Status != "CANCELLED" {
		// if attempt == 5 {
		// 	time.Sleep(5 * time.Second)
		// 	attempt = 0
		// }
		time.Sleep(1 * time.Second)
		orderData, _ = apiClient.GetOrderById(map[string]interface{}{"order_id": orderId})
		// attempt += 1
	}
	return orderData
}

func CancelAllOpenOrders(apiClient *api.ApiTradingClient) {
	openOrders, err := apiClient.GetOpenOrders(map[string]interface{}{"open": true})
	if err != nil {
		common.Log(common.WARNING, "Error getting all open orders: %v", err)
		return
	}
	for _, order := range openOrders {
		orderData := CancelOrderById(apiClient, order.Id)
		coin, _ := common.ExtractCoinNameFromSymbol(orderData.Symbol)

		if orderData.ExecutedQuantity > 0 {
			if orderData.Side == "BUY" {
				orderPriceInINR := orderData.Price
				if common.IsUSDTBaseExchange(orderData.Exchange) {
					common.ReadWriteMutex.RLock()
					buyCommissionFee := common.CommissionFee[orderData.Exchange]
					usdtBidAskPrice := common.UsdtBidAskPrice
					common.ReadWriteMutex.RUnlock()

					orderPriceInINR *= usdtBidAskPrice["average"]

					var usdtUsed float64

					tradeVolume := orderData.Price * orderData.ExecutedQuantity
					usdtCommissionAmount := tradeVolume * buyCommissionFee
					usdtTDSAmount := tradeVolume * common.TDSDetectionPercent
					usdtUsed = tradeVolume + usdtCommissionAmount + usdtTDSAmount

					UpdateAvgBuyPrice(common.BaseUSDT, usdtBidAskPrice["bid"], usdtUsed, false)
				}
				UpdateAvgBuyPrice(coin, orderPriceInINR, orderData.ExecutedQuantity, true)
			} else {
				redisSellOrders := common.ReadOrderDetailsFromRedis(coin)
				buyPrice, exists := redisSellOrders[orderData.Id]
				if !exists {
					continue
				}
				common.RemoveOrderIDFromRedis(coin, orderData.Id)

				if common.IsUSDTBaseExchange(orderData.Exchange) {
					updateUSDTAvgBuyPriceForClosedSellOrder(orderData)
				}

				if orderData.RemainingQuantity > 0 {
					UpdateAvgBuyPrice(coin, buyPrice, orderData.RemainingQuantity, true)
				}
			}
		}

	}
}
