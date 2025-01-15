package main

import (
	"time"
	"trade_bot/internal/api"
	"trade_bot/internal/arbitrage"
	"trade_bot/internal/common"
	"trade_bot/internal/configs"
	// "trade_bot/internal/spread"
	"trade_bot/internal/utils"
	"trade_bot/internal/websockets"
)

func main() {
	var client api.ApiTradingClient
	apiClient, err := client.NewApiTradingClientSingleton()

	if err != nil {
		common.Log(common.INFO, "Error in instantiating apiClient: %v", err)
		return
	}
	go utils.AddBlackListedCoin(apiClient)

	go utils.LogTDSInfo(apiClient)
	go utils.PeriodicUpdateCommissionFee(apiClient)

	go utils.UpdateCoinPrecisions(apiClient)
	common.Log(common.INFO, "Waiting for 75 seconds to select coins from csx based on trade volume.")
	time.Sleep(75 * time.Second)

	go websockets.IngestAllExchangeBidAsk()
	common.Log(common.INFO, "Waiting for 30 seconds to make sure we populate latest bidask values from websocket.")
	time.Sleep(30 * time.Second)

	go utils.PeriodicUpdateUSDTPrice(apiClient)
	time.Sleep(5 * time.Second)

	go utils.PeriodicUpdatePortfolioBalance(apiClient)
	time.Sleep(5 * time.Second)

	// spread.CancelSpread1StaleBuyOrders(apiClient, true)

	utils.RefreshOpenSellOrderIdInRedis(apiClient)
	time.Sleep(2 * time.Second)

	utils.UpdatePortfolioBalanceInRedis(apiClient, true)

	go utils.PeriodicUpdateRedisQuantity(apiClient)

	go arbitrage.PeriodicRebalanceUSDT(apiClient)
	time.Sleep(5 * time.Second)

	go utils.PeriodicSellPortfolioBalance(apiClient)
	time.Sleep(5 * time.Second)

	go utils.PeriodicCheckOpenSellOrders(apiClient)
	time.Sleep(5 * time.Second)

	go arbitrage.RunArbitrageForAllCoins(apiClient)

	// if configs.EnableSpread1 {
	// 	go spread.StartSpread1Trade(apiClient)

	// 	go spread.PeriodicUpdateSpread1Coins(apiClient)
	// 	common.Log(common.INFO, "Waiting for 30 seconds to allow the spread1 coins list to update.")
	// 	time.Sleep(30 * time.Second)

	// }

	// if configs.EnableSpread2 {
	// 	go spread.StartSpread2Trade(apiClient)
	// }

	if configs.AccountType == "client" {
		utils.PeriodicCheckBotStoppingCriteria(apiClient)
	} else {
		select {} // This will keep the main function running indefinitely
	}
}
