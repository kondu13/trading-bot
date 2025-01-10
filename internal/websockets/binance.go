package websockets

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"trade_go/internal/common"

	"github.com/gorilla/websocket"
)

type binanceDepthUpdate struct {
	EventType string     `json:"e"`
	EventTime int64      `json:"E"`
	Symbol    string     `json:"s"`
	Bids      [][]string `json:"b"`
	Asks      [][]string `json:"a"`
}

func initializeBinanceWebsocketConnection(coin string) *websocket.Conn {
	maxRetries := 10
	retries := 0
	for retries <= maxRetries {
		url := binanceBaseUrl + "/" + strings.ToLower(coin) + "usdt@depth"
		// common.Log(common.INFO, "%s -> %s: Connecting to binance websocket", common.ExcBinance, coin)
		conn, _, err := websocket.DefaultDialer.Dial(url, nil)

		if err != nil {
			common.Log(common.ERROR, "%s -> %s: Dial error: `%v`.\nReconnecting in %v...", common.ExcBinance, coin, err, reconnectDelay)
			time.Sleep(reconnectDelay)
			retries += 1
			continue
		}
		// common.Log(common.INFO, "%s -> %s: SUCCESS connected to binance websocket\n", common.ExcBinance, coin)
		return conn
	}
	return nil
}

func readDepthFromBinanceWebsocket(conn *websocket.Conn) (DepthUpdate, error) {
	/* Reads raw data from websocket and parse.
	Example of message:
	```
	{
		"e":"depthUpdate",
		"E":1722239994701,
		"s":"ETHUSDT",
		"U":34656912287,
		"u":34656912359,
		"b":[["3366.90000000","18.21700000"],["3366.89000000","0.70710000"]],
		"a":[["3366.91000000","73.35150000"],["3366.96000000","0.00160000"]]
	}
	```
	*/
	_, message, err := conn.ReadMessage()
	if err != nil {
		common.Log(common.ERROR, "%s : reading message: `%v`", common.ExcBinance, err)
		return DepthUpdate{}, err
	}

	var depthData binanceDepthUpdate
	if err := json.Unmarshal(message, &depthData); err != nil {
		common.Log(common.ERROR, "%s : unmarshalling JSON message: `%v`", common.ExcBinance, err)
		return DepthUpdate{}, err
	}
	if !isRawDepthValid(depthData.Asks) || !isRawDepthValid(depthData.Bids) {
		return DepthUpdate{}, nil
	}
	validDepthData := DepthUpdate{
		Symbol:    convertCurrencyPair(depthData.Symbol),
		Exchange:  common.ExcBinance,
		Timestamp: convertToTimestamp(depthData.EventTime),
		Bids:      depthData.Bids,
		Asks:      depthData.Asks,
	}
	return validDepthData, nil
}

func runBinanceWebsocketIngestion(ctx context.Context, coin string) {
	for {
		if ctx.Err() != nil {
            // Cleanup and exit the goroutine
            return
        }
		conn := initializeBinanceWebsocketConnection(coin)
		if conn == nil {
			common.Log(common.INFO, "%s -> %s: FAILURE in connecting to websocket", common.ExcBinance, coin)
			return
		}
		redisClient := common.GetRedisClient()

		for {
			if ctx.Err() != nil {
				conn.Close()
				return
			}
			depth, err := readDepthFromBinanceWebsocket(conn)
			if err != nil {
				common.Log(common.ERROR, "%s -> %s: ERROR reading depth from websocket: %v", common.ExcBinance, coin, err)
				break
			}
			if len(depth.Bids) == 0 || len(depth.Asks) == 0 {
				// common.Log(common.WARNING, "%s -> %s: Received empty depth: %v", common.ExcCoinswitch, coin, depth)
				continue
			} else {
				saveOrderBookToRedis(redisClient, depth)
			}
		}

		conn.Close()
		common.Log(common.ERROR, "%s -> %s: Websocket connection closed. Reconnecting in %v...", common.ExcBinance, coin, reconnectDelay)
		time.Sleep(reconnectDelay)
	}
}
