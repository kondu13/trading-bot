package arbitrage

const (
	MinArbitrageProfit     float64 = 1 // net profit in INR (profit - buy/sell commissions)
	MinRebalanceUSDTProfit float64 = 5  // net profit in INR (profit - buy/sell commissions)

	StaleBuyOrderTimeout float64 = 60 //seconds

	USDTMinBalanceINRRatio float64 = 0.1
	USDTMaxBalanceINRRatio float64 = 0.5

	USDTRebalanceMinMargin float64 = 0.001 // min arbitrage percent
)
