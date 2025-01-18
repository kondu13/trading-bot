package websockets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"trade_bot/internal/common"

	"github.com/gorilla/websocket"
)

type websocketEndpoint struct {
	Code string `json:"code"`
	Data struct {
		Token           string `json:"token"`
		InstanceServers []struct {
			Endpoint     string `json:"endpoint"`
			Protocol     string `json:"protocol"`
			Encrypt      bool   `json:"encrypt"`
			PingInterval int    `json:"pingInterval"`
			PingTimeout  int    `json:"pingTimeout"`
		} `json:"instanceServers"`
	} `json:"data"`
}

type Message struct {
	Topic   string    `json:"topic"`
	Type    string    `json:"type"`
	Data    depthData `json:"data"`
	Subject string    `json:"subject"`
}

type depthData struct {
	Asks      [][]string `json:"asks"`
	Bids      [][]string `json:"bids"`
	Timestamp int64      `json:"timestamp"` // Ensure this matches the expected JSON field name
}

type KucoinSubscribeMessage struct {
	ID             string `json:"id"`
	Type           string `json:"type"`
	Topic          string `json:"topic"`
	PrivateChannel bool   `json:"privateChannel"`
	Response       bool   `json:"response"`
}

func initializeKucoinWebsocketConnection() *websocket.Conn {
	maxRetries := 10
	retries := 0
	for retries <= maxRetries {
		url := "https://api.kucoin.com/api/v1/bullet-public"
		req, err := http.NewRequest("POST", url, bytes.NewBuffer([]byte("")))
		if err != nil {
			continue
		}

		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			common.Log(common.ERROR, "failed to read response body: %v", err)
			common.Log(common.ERROR, "Reconnecting websocket for %s in %v...", common.ExcKucoin, reconnectDelay)
			time.Sleep(reconnectDelay)
			retries += 1
			continue
		}

		if resp.StatusCode != http.StatusOK {
			common.Log(common.ERROR, "received non-OK HTTP status: %v", err)
			common.Log(common.ERROR, "Reconnecting websocket for %s in %v...", common.ExcKucoin, reconnectDelay)
			time.Sleep(reconnectDelay)
			retries += 1
			continue
		}

		var wsEndpoint websocketEndpoint
		if err := json.Unmarshal(body, &wsEndpoint); err != nil {
			common.Log(common.ERROR, "failed to unmarshal response: %v", err)
			common.Log(common.ERROR, "Reconnecting websocket for %s in %v...", common.ExcKucoin, reconnectDelay)
			time.Sleep(reconnectDelay)
			retries += 1
			continue
		}

		if wsEndpoint.Code != "200000" {
			common.Log(common.ERROR, "failed to get WebSocket endpoint: %v", err)
			common.Log(common.ERROR, "Reconnecting websocket for %s in %v...", common.ExcKucoin, reconnectDelay)
			time.Sleep(reconnectDelay)
			retries += 1
			continue
		}

		endpoint := wsEndpoint.Data.InstanceServers[0].Endpoint
		token := wsEndpoint.Data.Token
		u := fmt.Sprintf("%s?token=%s", endpoint, token)

		conn, _, err := websocket.DefaultDialer.Dial(u, nil)
		if err != nil {
			common.Log(common.ERROR, "%v", err)
			common.Log(common.ERROR, "Reconnecting websocket for %s in %v...", common.ExcKucoin, reconnectDelay)
			time.Sleep(reconnectDelay)
			retries += 1
			continue
		} else {
			_, message, err := conn.ReadMessage()
			msg := string(message)
			if err != nil {
				common.Log(common.ERROR, "%v", err)
				break
			}
			if strings.Contains(msg, "welcome") {
				// common.Log(common.INFO, "Received connection success message for %s websocket : `%v`", common.ExcKucoin, msg)
			} else {
				common.Log(common.ERROR, "Received unexpected message from %s websocket : `%v`", common.ExcKucoin, msg)
				break
			}
			// common.Log(common.INFO, "SUCCESS connected to %s websocket!", common.ExcKucoin)
			return conn
		}
	}
	return nil
}

func subscribeKucoinOrderBook(conn *websocket.Conn, coins []string) error {
	for _, coin := range coins {
		subscribeMsg := KucoinSubscribeMessage{
			ID:       "1541568410805", // Adjust the ID to be unique if needed
			Type:     "subscribe",
			Topic:    fmt.Sprintf("/spotMarket/level2Depth5:%s-USDT", coin),
			Response: true,
		}

		subscribeDataJSON, err := json.Marshal(subscribeMsg)
		if err != nil {
			return fmt.Errorf("%s -> %s: Failed to marshal subscribe data: %w", common.ExcKucoin, coin, err)
		}

		subscriptionEventMessage := string(subscribeDataJSON)

		if err := conn.WriteMessage(websocket.TextMessage, []byte(subscriptionEventMessage)); err != nil {
			return fmt.Errorf("%s -> %s: Failed to send subscription message: %w", common.ExcKucoin, coin, err)
		}

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				return err
			}

			msg := string(message)

			if strings.Contains(msg, "ack") {
				// Successfully subscribed to the coin
				// common.Log(common.INFO, "Kucoin -> %s: SUCCESS Received subscription message.", coin)
				break
			} else if !strings.Contains(msg, "level2") {
				common.Log(common.ERROR, "%v", msg)
				return nil
			}
		}
	}
	return nil
}

func readDepthFromKucoinWebsocket(conn *websocket.Conn) (DepthUpdate, error) {
	_, message, err := conn.ReadMessage()
	if err != nil {
		common.Log(common.ERROR, "%s: reading raw websocket message : %v", common.ExcKucoin, err)
		return DepthUpdate{}, err
	}

	var msg Message
	if err := json.Unmarshal(message, &msg); err != nil {
		common.Log(common.ERROR, "%s: unmarshalling JSON: %v", common.ExcKucoin, err)
		return DepthUpdate{}, nil
	}
	if !isRawDepthValid(msg.Data.Asks) || !isRawDepthValid(msg.Data.Bids) {
		return DepthUpdate{}, nil
	}
	if msg.Subject == "level2" {
		parts := strings.Split(msg.Topic, ":")
		currencyPair := ""
		if len(parts) > 1 {
			currencyPair = parts[1]
		}

		output := DepthUpdate{
			Symbol:    convertCurrencyPair(currencyPair),
			Exchange:  common.ExcKucoin,
			Timestamp: convertToTimestamp(msg.Data.Timestamp),
			Bids:      msg.Data.Bids,
			Asks:      msg.Data.Asks,
		}
		return output, nil
	}

	return DepthUpdate{}, nil
}

func connectToKuCoinWebsocket(coins []string) *websocket.Conn {
	exchange := common.ExcKucoin

	conn := initializeKucoinWebsocketConnection()
	if conn == nil {
		common.Log(common.ERROR, "%v -> %s: FAILURE to connect to websocket.", exchange, coins)
		return nil
	}
	err := subscribeKucoinOrderBook(conn, coins)
	if err != nil {
		common.Log(common.ERROR, "%v -> %s: FAILURE to subscribe to order book: %v", exchange, coins, err)
		return nil
	}
	// common.Log(common.INFO, "%s -> %s: Websocket connection successfull", exchange, coins)
	return conn
}

func pingServer(ctx context.Context, wg *sync.WaitGroup, conn *websocket.Conn) {
	defer wg.Done()
	ticker := time.NewTicker(time.Duration(6) * time.Second)
	for range ticker.C {
		if ctx.Err() != nil {
			conn.Close()
			return
		}
		if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
			common.Log(common.ERROR, "Kucoin: Error sending ping: %v", err)
			conn.Close()
			return
		}
	}
}

func batchRunKucoinWebsocketIngestion(ctx context.Context, wg *sync.WaitGroup, coin []string) {
	defer wg.Done()
	var childWg sync.WaitGroup
	for {
		conn := connectToKuCoinWebsocket(coin)
		if conn == nil {
			common.Log(common.ERROR, "%s -> %s: FAILURE in connecting to websocket.", common.ExcKucoin, coin)
			return
		}
		redisClient := common.GetRedisClient()

		childWg.Add(1)
		go pingServer(ctx, &childWg, conn)

		for {
			if ctx.Err() != nil {
				conn.Close()
				return
			}
			depth, err := readDepthFromKucoinWebsocket(conn)
			if err != nil {
				// common.Log(common.ERROR, "Kucoin: ERROR reading depth from WebSocket: %v", err)
				break
			}

			if len(depth.Bids) == 0 || len(depth.Asks) == 0 {
				// common.Log(common.WARNING, "%s -> %s: Received empty depth: %v", common.ExcKucoin, coin, depth)
				continue
			} else {
				saveOrderBookToRedis(redisClient, depth)
			}
		}
		conn.Close()
		common.Log(common.ERROR, "Kucoin: WebSocket connection closed. Reconnecting in %v...", reconnectDelay)
		time.Sleep(reconnectDelay)
	}
}

func runKucoinWebsocketIngestion(ctx context.Context, coins []string) {
	// Initializes the websocket connections for all coins in batch.
	var batchCoins = []string{}
	var childWg sync.WaitGroup
	for _, coin := range coins {
		if len(batchCoins) < 4 {
			batchCoins = append(batchCoins, coin)
		} else {
			childWg.Add(1)
			go batchRunKucoinWebsocketIngestion(ctx, &childWg, coins)
			batchCoins = []string{}
		}
	}
	childWg.Wait()
	if ctx.Err() != nil {
		common.Log(common.WARNING, "Killing KuCoin websocket goroutines.")
		return
	}
}
