package configs

const (
	AccountId   string = "ayushi"
	AccountType string = "regular" // "client" or "regular"

	CommissionRate float64 = 0.22 // original is 0.2

	// Default sell configs.
	MinPortfolioSellProfitPercent float64 = 0.005 // Min percentage of profit we are seeking relative to the spent amount.
	SellProfitStopLoss            float64 = -0.05 // % Loss below which we need to sell at market order.

	// Arbitrage trade configs.
	MinProfitPercentOfSpendingAmount float64 = 0.009 // Min percentage of profit we are seeking relative to the spent amount.
	MaxProfitPercentOfSpendingAmount float64 = 100   // Max percentage of profit we are seeking relative to the spent amount.
	// Max order amount percent of INR balance, used when buy from INR base exchange.
	INRBaseTradeAmountPercent float64 = 0.25
	// Max order amount percent of USDT balance, used when buy from USDT base exchange.
	USDTBaseTradeAmountPercent float64 = 0.5

	//Spread-1 trade configs.
	EnableSpread1               bool    = true
	Spread1CoinCount            int     = 10
	MinArbitrageForSpread1Coins float64 = 0.001
	MinSpread1BuyProfitPercent  float64 = 0.013
	MinSpread1SellProfitPercent float64 = 0.009
	Spread1DefaultOrderAmount   float64 = 10000

	// Spread-2 trade configs
	EnableSpread2               bool    = true
	MinSpread2BuyProfitPercent  float64 = 0.015
	MinSpread2SellProfitPercent float64 = 0.011
	Spread2DefaultOrderAmount   float64 = 10000
)

var (
	Spread1OrderAmount = map[string]float64{"MATIC": 10000}

	Spread2OrderAmount = map[string]float64{}
	Spread2Coins       = []string{"XRP", "ETH", "SOL", "ADA"}

	// NOT traded coins.
	NotTradedCoins    = []string{"LUNA2", "LUNA"}
	NotSellAtStopLoss = []string{"BTC"} // Holding these coins is OK.
)
