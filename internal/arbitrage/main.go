package arbitrage

import (
	"context"
	"time"
	"trade_bot/internal/api"
	"trade_bot/internal/common"
)

func RunArbitrageForAllCoins(apiClient *api.ApiTradingClient) {
	/* Checks every 5 minute to either start or stop an arbitrage goroutine.
	 */
	var activeGoroutines map[string]context.CancelFunc = make(map[string]context.CancelFunc)
	// firstTime := true
	for {
		common.ReadWriteMutex.RLock()
		commonArbitrageCoins := common.CommonArbitrageCoins
		common.ReadWriteMutex.RUnlock()

		var outDatedCoins []string
		for coin := range activeGoroutines {
			if !common.DoesListContainsString(commonArbitrageCoins, coin) {
				outDatedCoins = append(outDatedCoins, coin)
			}
		}
		for _, coin := range outDatedCoins {
			common.Log(common.INFO, "Stopping and removing Coinswitch WebSocket goroutine for coin: %s", coin)
			cancelFunc := activeGoroutines[coin]
			cancelFunc()                   // Cancel the goroutine
			delete(activeGoroutines, coin) // Remove from the map
		}

		// Start new goroutines for coins that are newly added
		for _, coin := range commonArbitrageCoins {
			if _, exists := activeGoroutines[coin]; !exists {
				// if !firstTime {
				// 	common.Log(common.INFO, "Starting new arbitrage goroutine for coin: %s", coin)
				// }
				ctx, cancel := context.WithCancel(context.Background())
				activeGoroutines[coin] = cancel
				go runArbitrageForSingleCoin(ctx, apiClient, coin)
			}
		}
		// firstTime = false
		time.Sleep(5 * time.Minute)
	}
}

func PeriodicRebalanceUSDT(apiClient *api.ApiTradingClient) {
	rebalanceUSDT(apiClient)
	for range time.Tick(2 * time.Minute) {
		rebalanceUSDT(apiClient)
	}
}
