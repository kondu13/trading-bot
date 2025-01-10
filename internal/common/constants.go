package common

import "sync"

const(
	UserConfigPath					 string = "internal/configs/user_config.json"
	ApiKeysJsonPath 			   string = "internal/configs/api_keys.json"
	ExhangePrecisionsPath 	 string = "internal/configs/exchange_precisions.json"
	CommonArbitrageCoinsPath string = "internal/configs/arbitrage_coins.json"

	BaseINR  string = "INR"
	BaseUSDT string = "USDT"
	
	ExcCoinswitch string = "coinswitchx"
	ExcWazirx 		string = "wazirx"
	ExcBinance 		string = "c2c1"
	ExcKucoin 		string = "c2c2"

	MaxDepth int = 1

	StaleWebsocketReadingTimeThreshold float64 = 30 //seconds 

	MinValidOrderAmount 				float64 = 1000
	MinValidorderAmountInrBase 	float64 = 200

	SellPortfolioMinProfit 	float64 = 5 // exact proft in INR
	TDSDetectionPercent 		float64 = 0.01
	MaxWaitingTime 					float64 = 60 //seconds

	RedisOrderKeyVar = "%s:OrderID"
	RedisAvgBuyPriceKeyVar = "%s:AvgBuyPrice"
	RedisSpreadBuyOrderId = "%s:Spread1BuyOrderId"

	// Select arbitrage coins with high volume
	ArbitrageCoinTradeVolumeHour int = 24
	ArbitrageCoinTradeVolumeMin int = 15
	ArbitrageCoinMinVolumePercentile float64 = 20

	// Interval type
	IntervalHour string = "hour"
	IntervalMinute string = "minute"
	IntervalSecond string = "second"
)

var Spread1coins = []string{}
var Spread1SellExchanges = []string{ExcBinance, ExcKucoin}

var USDTBaseExchanges []string = []string{ExcBinance, ExcKucoin}
var AllExchanges []string = []string{ExcCoinswitch, ExcBinance, ExcKucoin}

var ReadWriteMutex sync.RWMutex // RWMutex to protect shared data

// Saves latest coin precisions on each exchange {Exchange -> Coin Pair -> Precision Type -> Value}
var AllExchangeCoinPrecisions map[string]map[string]map[string]float64 

// Saves valid coin list on each exchange
var AllExchangeCoins map[string][]string

// Arbitrage coins
var commonArbitrageCoins []string

// Save latest coin balance in user's portfolio and open sell orders
var AccountPortfolioBalance map[string]PortfolioCoinBalance