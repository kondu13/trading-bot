package common

import "time"

type AccountKeys struct{
	SecretKey string `json:"secret_key"`
	ApiKey    string `json:"api_key"`
}

type CoinMeta struct {
	Coin               string  // Name of the coin
	Price              float64 // Current price of the coin in INR with average market price of INR.
	PriceInUsdt        float64 // Current price of the coin in USDT
	PriceWithUsdtRedis float64 // Price converted using buyAvgPrice of USDT from redis.
	PriceWithUsdtAsk   float64 // Price of the coin with respect to USDT (Ask)
	PriceWithUsdtBid   float64 // Price of the coin with respect to USDT (Bid)
	Quantity           float64 // Quantity of the coin available
	Exchange           string  // Exchange where the coin's data is fetched from
	Base               string  // Base currency (e.g., INR, USDT)
}

// BidAskDepth represents the depth of bid and ask orders for a coin.
type BidAskDepth struct {
	Asks []CoinMeta // List of ask orders for the coin
	Bids []CoinMeta // List of bid orders for the coin
}

// Quantity and INR value of a specific coin present in portfolio
type PortfolioCoinBalance struct {
	Quantity float64 // Holding quantity of coin.
	Amount   float64 // Holding value in INR of the coin.
}

// Order data received from CSX rest API.
type CSXOrder struct {
	Id                string
	Symbol            string
	Status            string // OPEN or PARTIALLY_EXECUTED or CLOSED
	Side              string // SELL or BUY
	Exchange          string
	Price             float64
	OriginalQuantity  float64
	ExecutedQuantity  float64
	RemainingQuantity float64
	CreatedTime       time.Time
	UpdatedTime       time.Time
}

type ArbitrageOpportunity struct {
	BuyOffer  CoinMeta
	SellOffer CoinMeta
	// TradeArbitrageAmount float64 // quantity * arbitrage_margin
	NetProfit float64 // netprofit including the commission and usdtToinr conversion loss.
}

type CandleStickData struct {
	Symbol    string
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Interval  float64
	Volume    float64
	StartTime time.Time
	CloseTime time.Time
}

type CoinTradeVolume struct {
	Coin        string
	Exchange    string
	VolumeInINR float64
}

type FinancialInfo struct {
	InvestedValue float64
	TotalTDS      float64
	CurrentValue  float64
	PnL           float64
}