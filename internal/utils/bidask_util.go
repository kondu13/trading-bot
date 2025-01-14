package utils

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"trade_bot/internal/api"
	"trade_bot/internal/common"
	"trade_bot/internal/websockets"
)

func getEmptyBidAsk(coin string, exchange string) common.BidAskDepth {
	return common.BidAskDepth{
		Asks: []common.CoinMeta{
			{
				Coin:     coin,
				Exchange: exchange,
			},
		},
		Bids: []common.CoinMeta{
			{
				Coin:     coin,
				Exchange: exchange,
			},
		},
	}
}

func ConvertUsdtToInr(price float64, side string) float64 {
	/* Converts USDT price to INR value.
	- "bid": Uses current market "bid" price of USDT/INR on csx.
	- "ask": Uses current market "ask" price of USDT/INR on csx.
	- "average": Uses current market "ask"+"bid" avg price of USDT/INR on csx.
	- "redis": Uses accumulated AvgBuyPrice stored in local redis.
	*/
	common.ReadWriteMutex.RLock()
	usdtBidAskPrice := common.UsdtBidAskPrice
	common.ReadWriteMutex.RUnlock()
	if side == "bid" {
		return price * usdtBidAskPrice["bid"]
	} else if side == "ask" {
		return price * usdtBidAskPrice["ask"]
	} else if side == "redis" {
		avgBuyPrice, _ := common.ReadAvgBuyPriceFromRedis(common.BaseUSDT)
		if avgBuyPrice != 0 {
			return avgBuyPrice * price
		}
		// common.Log(common.WARNING, "The average buy price for USDT is zero. Returning market price for INR conversion.")
	}
	return price * usdtBidAskPrice["average"]
}

func ConvertToPreciseQuantity(quantity float64, coin, exchange string) float64 {
	common.ReadWriteMutex.RLock()
	allCoinExchangePrecisions := common.AllExchangeCoinPrecisions
	common.ReadWriteMutex.RUnlock()

	coinPrecisions := allCoinExchangePrecisions[exchange][coin]
	quantityPrecision := coinPrecisions["base"]
	factor := math.Pow(10, quantityPrecision)
	return math.Trunc(quantity*factor) / factor
}

func convertRawRedisDepthToCoinMeta(coin, exchange string, rawDepths [][]string, depth int, isUsdtExchange bool) []common.CoinMeta {
	multipleDepthCoinMeta := []common.CoinMeta{}
	rawDepthCount := len(rawDepths)

	for _, rawDepth := range rawDepths[:min(depth, rawDepthCount)] {
		price, err := strconv.ParseFloat(rawDepth[0], 64)
		if err != nil {
			return multipleDepthCoinMeta
		}
		quantity, err := strconv.ParseFloat(rawDepth[1], 64)
		if err != nil {
			return multipleDepthCoinMeta
		}
		multipleDepthCoinMeta = append(multipleDepthCoinMeta, populateCoinMeta(
			coin, exchange, isUsdtExchange, price, quantity,
		))
	}
	return multipleDepthCoinMeta
}

func readBidsAndAsksFromRedis(coin, exchange string, depth int) (common.BidAskDepth, error) {
	/* Perform the following steps
	1. Read DepthUdate.
	2. Convert to CoinMeta
	*/
	redisClient := common.GetRedisClient()
	coinRawBidAAsk, err := websockets.ReadOrderBookFromRedis(redisClient, coin, exchange)
	if err != nil {
		common.Log(common.ERROR, "reading %s bidask from %s from redis: `%v`", coin, exchange, err)
		return getEmptyBidAsk(coin, exchange), err
	}
	readingTimeDifference := time.Since(coinRawBidAAsk.Timestamp).Seconds()
	if readingTimeDifference > common.StaleWebsocketReadingTimeThreshold {
		return getEmptyBidAsk(coin, exchange), fmt.Errorf("%s -> %s: stale value found in redis with time difference: %.3f seconds", coin, exchange, readingTimeDifference)
	}

	isUSDTExchange := common.DoesListContainsString(common.USDTBaseExchanges, exchange)

	coinMetaAsks := convertRawRedisDepthToCoinMeta(coin, exchange, coinRawBidAAsk.Asks, depth, isUSDTExchange)
	coinMetaBids := convertRawRedisDepthToCoinMeta(coin, exchange, coinRawBidAAsk.Bids, depth, isUSDTExchange)

	if len(coinMetaAsks) == 0 || len(coinMetaBids) == 0 {
		return getEmptyBidAsk(coin, exchange), fmt.Errorf("ERROR in fetching bidask from websocket for %s : %s", coin, exchange)
	}
	return common.BidAskDepth{
		Asks: coinMetaAsks, Bids: coinMetaBids,
	}, nil
}

func populateCoinMeta(coin, exchange string, isUSDTExchange bool, price, quantity float64) common.CoinMeta {
	var priceWithUsdtAsk, priceWithUsdtBid, priceInUsdt, priceWithUsdtRedis float64
	if isUSDTExchange {
		priceInUsdt = price
		priceWithUsdtAsk = ConvertUsdtToInr(price, "ask")
		priceWithUsdtBid = ConvertUsdtToInr(price, "bid")
		priceWithUsdtRedis = ConvertUsdtToInr(price, "redis")
		price = ConvertUsdtToInr(price, "average")
	}

	return common.CoinMeta{
		Coin:               coin,
		Price:              price,
		PriceInUsdt:        priceInUsdt,
		PriceWithUsdtRedis: priceWithUsdtRedis,
		PriceWithUsdtAsk:   priceWithUsdtAsk,
		PriceWithUsdtBid:   priceWithUsdtBid,
		Quantity:           ConvertToPreciseQuantity(quantity, coin, exchange),
		Exchange:           exchange,
		Base:               common.BaseINR,
	}
}

func GetBidsAndAsks(apiClient *api.ApiTradingClient, coin, exchange string, depth int) (common.BidAskDepth, error) {
	/*
		Fetch the latest asks and bids of a coin on exchange.
	*/
	common.ReadWriteMutex.RLock()
	exchangeWiseCoins := common.AllExchangeCoins
	common.ReadWriteMutex.RUnlock()

	exchangeCoins, exists := exchangeWiseCoins[exchange]
	if !exists {
		return getEmptyBidAsk(coin, exchange), fmt.Errorf("invalid exchange found: %s", exchange)
	}
	coinExists := common.DoesListContainsString(exchangeCoins, coin)
	if !coinExists {
		return getEmptyBidAsk(coin, exchange), fmt.Errorf("%s does not exist on %s", coin, exchange)
	}

	if depth == 0 {
		depth = common.MaxDepth
	}
	websocketBidAsks, err := readBidsAndAsksFromRedis(coin, exchange, depth)
	if err != nil {
		// common.Log(common.ERROR, "reading websocket bidask for %s : %s: `%v`", coin, exchange, err)
	} else if len(websocketBidAsks.Asks) == 0 || len(websocketBidAsks.Bids) == 0 {
		common.Log(common.ERROR, "reading websocket bidask for %s : %s", coin, exchange)
	} else {
		return websocketBidAsks, nil
	}

	isUSDTExchange := common.IsUSDTBaseExchange(exchange)

	symbol := common.CreateExchangeSymbolForCSX(coin, exchange)

	params := map[string]interface{}{
		"exchange": exchange,
		"symbol":   symbol,
	}

	response, err := apiClient.GetDepth(params)
	if err != nil {
		return getEmptyBidAsk(coin, exchange), err
	}

	data, ok := response["data"].(map[string]interface{})
	if !ok || data == nil {
		return getEmptyBidAsk(coin, exchange), errors.New("invalid data in response")
	}

	bids, ok := data["bids"].([]interface{})
	if !ok {
		return getEmptyBidAsk(coin, exchange), errors.New("invalid bids data")
	}

	asks, ok := data["asks"].([]interface{})
	if !ok {
		return getEmptyBidAsk(coin, exchange), errors.New("invalid asks data")
	}

	output := common.BidAskDepth{Asks: []common.CoinMeta{}, Bids: []common.CoinMeta{}}
	currentDepth := 0

	if len(asks) == 0 || len(bids) == 0 {
		return getEmptyBidAsk(coin, exchange), errors.New("empty bids or asks data")
	}

	for currentDepth < depth {
		currentAsk := asks[currentDepth].([]interface{})
		askPrice, err := strconv.ParseFloat(currentAsk[0].(string), 64)
		if err != nil {
			return getEmptyBidAsk(coin, exchange), err
		}
		askQuantity, err := strconv.ParseFloat(currentAsk[1].(string), 64)
		if err != nil {
			return getEmptyBidAsk(coin, exchange), err
		}
		output.Asks = append(output.Asks, populateCoinMeta(
			coin, exchange, isUSDTExchange, askPrice, askQuantity,
		))

		currentBid := bids[currentDepth].([]interface{})
		bidPrice, err := strconv.ParseFloat(currentBid[0].(string), 64)
		if err != nil {
			return getEmptyBidAsk(coin, exchange), err
		}
		bidQuantity, err := strconv.ParseFloat(currentBid[1].(string), 64)
		if err != nil {
			return getEmptyBidAsk(coin, exchange), err
		}
		output.Bids = append(output.Bids, populateCoinMeta(
			coin, exchange, isUSDTExchange, bidPrice, bidQuantity,
		))

		currentDepth++
	}
	return output, nil
}

func FetchExchangeWiseBidAskInSequence(apiClient *api.ApiTradingClient, coin string) map[string]common.BidAskDepth {
	/*
		Fetches bidask for all exchanges in sequence.
	*/
	exchangeWiseBidAsk := make(map[string]common.BidAskDepth)
	for _, exchange := range common.AllExchanges {
		exchangeBidAskDepth, err := GetBidsAndAsks(apiClient, coin, exchange, common.MaxDepth)
		if err != nil {
			continue
		}

		if len(exchangeBidAskDepth.Asks) > 0 && len(exchangeBidAskDepth.Bids) > 0 {
			exchangeWiseBidAsk[exchange] = exchangeBidAskDepth
		}
	}
	return exchangeWiseBidAsk
}

func exchangeBidAskInParallel(apiClient *api.ApiTradingClient, coinMeta common.CoinMeta, depth int, bidaskChannel chan<- common.BidAskDepth, wg *sync.WaitGroup) {
	defer wg.Done()

	exchangeBidAsk, err := GetBidsAndAsks(apiClient, coinMeta.Coin, coinMeta.Exchange, depth)
	if err != nil {
		return
	}
	bidaskChannel <- exchangeBidAsk
}

func FetchExchangeWiseBidAskInParallel(apiClient *api.ApiTradingClient, coin string) map[string]common.BidAskDepth {
	/*
		Fetches bidask for all exchanges in parallel.
	*/
	depth := 2

	// Create a wait group to wait for all goroutines to finish
	var wg sync.WaitGroup
	wg.Add(len(common.AllExchanges))

	// Create a channel to receive results from goroutines
	exchangesBidAskCh := make(chan common.BidAskDepth)

	// Launch goroutines for each exchange
	for _, exchange := range common.AllExchanges {

		coinMeta := common.CoinMeta{
			Coin:     coin,
			Base:     common.BaseINR,
			Exchange: exchange,
		}

		go exchangeBidAskInParallel(apiClient, coinMeta, depth, exchangesBidAskCh, &wg)
	}

	// Close the channel once all goroutines are done
	go func() {
		wg.Wait()
		close(exchangesBidAskCh)
	}()

	// Receive results from channel
	exchangeWiseBidAsk := make(map[string]common.BidAskDepth)

	for exchangeBidAskDepth := range exchangesBidAskCh {
		if len(exchangeBidAskDepth.Asks) > 0 && exchangeBidAskDepth.Asks[0].Quantity != 0 {
			asks := exchangeBidAskDepth.Asks
			exchange := asks[0].Exchange
			exchangeWiseBidAsk[exchange] = exchangeBidAskDepth
		}
	}
	return exchangeWiseBidAsk
}
