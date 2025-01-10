package websockets

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"trade_go/internal/common"

	"github.com/gorilla/websocket"
)

/*
1. Connect to coinswitch websocket.
2. Send and read message for the namespace.
3. Send and read message for the subscription message.
*/

const CSXBaseCurrency string = "INR"

type CSXSubscribeMessage struct {
	Event string `json:"event"`
	Pair  string `json:"pair"`
}

func initializeCSXWebsocketConnection() *websocket.Conn {
	maxRetries := 10
	retries := 0
	for retries < maxRetries {
		u := url.URL{
			Scheme:   "wss",
			Host:     coinswitchBaseUrl,
			Path:     coinswitchHandshakePath,
			RawQuery: "EIO=4&transport=websocket",
		}
		conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
		if err != nil {
			common.Log(common.ERROR, "%v", err)
			common.Log(common.ERROR, "Reconnecting websocket for %s in %v...", common.ExcCoinswitch, reconnectDelay)
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
			if strings.Contains(msg, "maxPayload") {
				// common.Log(common.INFO, "Received connection success message for %s websocket : `%v`", common.ExcCoinswitch, msg)
			} else {
				common.Log(common.ERROR, "Received unexpected message from %s websocket : `%v`", common.ExcCoinswitch, msg)
				break
			}
			// common.Log(common.INFO, "SUCCESS connected to %s websocket!", common.ExcCoinswitch)
			return conn
		}
	}
	return nil
}

func subscribeCSXOrderBook(conn *websocket.Conn, coin string) error {
	subscribeMsg := CSXSubscribeMessage{
		Event: "subscribe",
		Pair:  fmt.Sprintf("%s,%s", coin, CSXBaseCurrency),
	}
	subscribeDataJSON, err := json.Marshal(subscribeMsg)
	if err != nil {
		return fmt.Errorf("%s -> %s: Failed to marshal subscribe data: %w", common.ExcCoinswitch, coin, err)
	}

	namespace := "/" + common.ExcCoinswitch

	subscriptionEventMessage := fmt.Sprintf("42%s,[\"%s\",%s]", namespace, coinswitchOrderBookEvent, string(subscribeDataJSON))

	// common.Log(common.INFO, "%s -> %s: Sending subscription message: `%s`", common.ExcCoinswitch, coin, subscriptionEventMessage)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(subscriptionEventMessage)); err != nil {
		return fmt.Errorf("%s -> %s: Failed to send subscription message: %w", common.ExcCoinswitch, coin, err)
	}
	_, message, err := conn.ReadMessage()
	msg := string(message)
	if err != nil {
		return err
	}
	if strings.Contains(msg, "success") {
		// common.Log(common.INFO, "%s -> %s: SUCCESS Received subscription message.", common.ExcCoinswitch, coin)

	} else {
		common.Log(common.ERROR, "%v", msg)
		return nil
	}
	// common.Log(common.INFO, "%s -> %s: SUCCESS subscribed to `%s` namespace.", common.ExcCoinswitch, coin, namespace)
	return nil
}

func connectCSXWebsocketToNamespace(conn *websocket.Conn, coin string) error {
	maxRetries := 10
	retries := 0
	connectionSuccess := false

	namespace := "/" + common.ExcCoinswitch
	namespaceMsg := fmt.Sprintf("40%s,", namespace)

	for retries < maxRetries {

		if err := conn.WriteMessage(websocket.TextMessage, []byte(namespaceMsg)); err != nil {
			common.Log(common.ERROR, "%s -> %s: FAILURE to send namespace connection message: %s -> %v", common.ExcCoinswitch, coin, namespaceMsg, err)
			conn.Close()
			common.Log(common.INFO, "%s -> %s: Reconnecting in %v...", common.ExcCoinswitch, coin, reconnectDelay)
			time.Sleep(reconnectDelay)
			retries += 1
			continue
		}
		// common.Log(common.INFO, "%s -> %s: SUCCESS namespace connection msg: `%s` sent.", common.ExcCoinswitch, coin, namespaceMsg)

		_, message, err := conn.ReadMessage()
		msg := string(message)
		if err != nil {
			common.Log(common.ERROR, "%s -> %s: FAILURE to receive response for namespace: %s -> `%v`", common.ExcCoinswitch, coin, namespaceMsg, err)
			break
		}
		if strings.Contains(msg, "40") {
			// common.Log(common.INFO, "%s -> %s: SUCCESS namespace connection msg: `%s` received.", common.ExcCoinswitch, coin, namespaceMsg)
			connectionSuccess = true
			break
		} else {
			common.Log(common.ERROR, "%s -> %s: FAILURE to receive valid response for namespace: %s -> msg: `%v`", common.ExcCoinswitch, coin, namespaceMsg, msg)
			break
		}
	}
	if connectionSuccess {
		return nil
	} else {
		return fmt.Errorf("failed to connect to namespace: %s", namespaceMsg)
	}
}

func connectToCSXWebsocket(coin string) *websocket.Conn {
	exchange := common.ExcCoinswitch

	conn := initializeCSXWebsocketConnection()
	if conn == nil {
		common.Log(common.ERROR, "%s -> %s: FAILURE to connect to websocket.", exchange, coin)
		return nil
	}
	err := connectCSXWebsocketToNamespace(conn, coin)
	if err != nil {
		common.Log(common.ERROR, "%s -> %s: FAILURE to connect to namespace.", exchange, coin)
		return nil
	}
	err = subscribeCSXOrderBook(conn, coin)
	if err != nil {
		common.Log(common.ERROR, "%s -> %s: FAILURE to subscribe to order book: %v", exchange, coin, err)
		return nil
	}
	// common.Log(common.INFO, "%s -> %s: Websocket connection successfull", exchange, coin)
	return conn
}

func readDepthFromCSXWebsocket(coin string, conn *websocket.Conn) (DepthUpdate, error) {
	_, message, err := conn.ReadMessage()
	if err != nil {
		common.Log(common.ERROR, "%s -> %s: reading raw websocket message : %v", common.ExcCoinswitch, coin, err)
		return DepthUpdate{}, err
	}
	/*
		Parse the raw message from websocket to only extract the valid JSON part.
		Example of message:
		```
		42/coinswitchx,[
		"FETCH_ORDER_BOOK_CS_PRO",
		{
			"s":"ETH,INR",
			"timestamp":1721980284679,
			"bids":[["292579","0.12238"]],
			"asks": [["316437","0.38573"]]
		}]
		```
		1. Find the index of starting of the depth message: `"{"s":`
		2. Extract the part of the message starting from the index found in step 1 to the end of the message - 1.
		3. Unmarshal the JSON part to the depthUpdate struct.
	*/
	prefixStr := "[\"FETCH_ORDER_BOOK_CS_PRO\",{"
	prefixStrIndexOffset := len(prefixStr) - 1
	depthJsonStartIndex := strings.Index(string(message), prefixStr)

	if depthJsonStartIndex == -1 {
		if len(message) == 1 && message[0] == 50 {
			if err := conn.WriteMessage(websocket.TextMessage, []byte("3")); err != nil {
				common.Log(common.ERROR, "%s -> %s: pong error: %v", common.ExcCoinswitch, coin, err)
				return DepthUpdate{}, err
			}
			return DepthUpdate{}, nil
		}
		return DepthUpdate{}, fmt.Errorf("%s -> %s: ERROR reading raw websocket message: valid JSON string not found", common.ExcCoinswitch, coin)
	}
	depthJsonStartIndex += prefixStrIndexOffset
	if depthJsonStartIndex > len(message) {
		return DepthUpdate{}, fmt.Errorf("%s -> %s: ERROR parsing websocket string: message: `%v`", common.ExcCoinswitch, coin, string(message))
	}

	validDepthJsonStr := message[depthJsonStartIndex : len(message)-1]

	var depthData DepthUpdate
	if err := json.Unmarshal([]byte(validDepthJsonStr), &depthData); err != nil {
		common.Log(common.ERROR, "%s -> %s: unmarshalling JSON: %v", common.ExcCoinswitch, coin, err)
		return DepthUpdate{}, err
	}
	if !isRawDepthValid(depthData.Asks) || !isRawDepthValid(depthData.Bids) {
		return DepthUpdate{}, nil
	}
	depthData.Symbol = convertCurrencyPair(depthData.Symbol)
	depthData.Timestamp = convertToTimestamp(depthData.RawTimestamp)
	depthData.Exchange = common.ExcCoinswitch
	// common.Log(common.INFO, "%v", depthData.Timestamp)
	return depthData, nil
}

func RunCSXWebsocketIngestion(ctx context.Context, coin string) {
	for {
		if ctx.Err() != nil {
			// Cleanup and exit the goroutine
			return
		}
		conn := connectToCSXWebsocket(coin)
		if conn == nil {
			common.Log(common.ERROR, "%s -> %s: FAILURE in connecting to websocket.", common.ExcCoinswitch, coin)
			return
		}
		redisClient := common.GetRedisClient()

		for {
			if ctx.Err() != nil {
				conn.Close()
				return
			}
			depth, err := readDepthFromCSXWebsocket(coin, conn)
			if err != nil {
				// common.Log(common.ERROR, "%s -> %s: reading depth from websocket: %v", common.ExcCoinswitch, coin, err)
				break
			}
			if len(depth.Bids) == 0 || len(depth.Asks) == 0 {
				// common.Log(common.WARNING, "%s -> %s: Received empty depth: %v", common.ExcCoinswitch, coin, depth)
				continue
			} else {
				saveOrderBookToRedis(redisClient, depth)
			}
			// common.Log(common.INFO, "%s -> %s: Received depth: %v", common.ExcCoinswitch, coin, depth.Timestamp)
		}

		conn.Close()
		// common.Log(common.ERROR, "%s -> %s: Websocket connection closed. Reconnecting in %v...", common.ExcCoinswitch, coin, reconnectDelay)
		time.Sleep(reconnectDelay)
	}
}
