package websockets

import "time"

const (
	reconnectDelay = 5 * time.Second

	bidAskDepth = 2

	redisDepthKeyVar = "%s:%s"

	coinswitchBaseUrl        = "ws.coinswitch.co"
	coinswitchHandshakePath  = "/pro/realtime-rates-socket/spot/coinswitchx"
	coinswitchOrderBookEvent = "FETCH_ORDER_BOOK_CS_PRO"

	binanceBaseUrl = "wss://stream.binance.com:9443/ws"
	kucoinBaseUrl  = "https://api.kucoin.com"
)
