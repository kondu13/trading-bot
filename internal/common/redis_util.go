package common

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/redis/go-redis/v9"
)

var (
	client *redis.Client
	once   sync.Once
	ctx    = context.Background()
)

func initClient() {
	client = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379", // Update with your Redis address
		Password: "",               // Set if you have a password
		DB:       0,                // Use default DB
	})
	_, err := client.Ping(context.Background()).Result()
	if err != nil {
		Log(ERROR, "connect to Redis: %v", err)
	}
}

// GetRedisClient returns the Redis client instance
func GetRedisClient() *redis.Client {
	once.Do(initClient)
	return client
}

func AppendOrderDetailsToRedis(coin string, orderID string, buyPrice float64) {
	redisClient := GetRedisClient()
	orderKey := fmt.Sprintf(RedisOrderKeyVar, coin)
	err := redisClient.HSet(ctx, orderKey, orderID, buyPrice).Err()
	if err != nil {
		Log(ERROR, "setting order hash: `%v`", err)
	}
}

func ReadOrderDetailsFromRedis(coin string) map[string]float64 {
	redisClient := GetRedisClient()
	orderKey := fmt.Sprintf(RedisOrderKeyVar, coin)
	sellOrders := map[string]float64{}
	result, err := redisClient.HGetAll(ctx, orderKey).Result()
	if err != nil {
		Log(ERROR, "getting order hash from Redis for the coin %v: %v", coin, err)
	} else if len(result) != 0 {
		for id, buyPrice := range result {
			buyPrice, err := strconv.ParseFloat(buyPrice, 64)
			if err != nil {
				Log(ERROR, "parsing buy price for order: `%s` from redis for %s: %v", id, coin, err)
				continue
			}
			sellOrders[id] = buyPrice
		}
	}
	return sellOrders
}

func RemoveOrderIDFromRedis(coin string, orderID string) {
	redisClient := GetRedisClient()
	orderKey := fmt.Sprintf(RedisOrderKeyVar, coin)
	err := redisClient.HDel(ctx, orderKey, orderID).Err()
	if err != nil {
		Log(ERROR, "deleting order hash: `%v`", err)
	}
}

func AppendSpreadOrderDetailsToRedis(coin string, spreadBuyOrderId string) {
	redisClient := GetRedisClient()
	coinKey := fmt.Sprintf(RedisSpreadBuyOrderId, coin)
	err := redisClient.HSet(ctx, coinKey, "BuyOrderId", spreadBuyOrderId).Err()
	if err != nil {
		Log(ERROR, "setting spread buy order hash: `%v`", err)
	}

}

func ReadSpreadOrderIdFromRedis(coin string) string {
	redisClient := GetRedisClient()
	coinKey := fmt.Sprintf(RedisSpreadBuyOrderId, coin)
	buyOrderId, err := redisClient.HGet(ctx, coinKey, "BuyOrderId").Result()
	if err != nil {
		return ""
	}
	return buyOrderId
}

func RemoveSpreadOrderIdFromRedis(coin string) {
	redisClient := GetRedisClient()
	coinKey := fmt.Sprintf(RedisSpreadBuyOrderId, coin)
	err := redisClient.HDel(ctx, coinKey, "BuyOrderId").Err()
	if err != nil {
		Log(ERROR, "deleting sell order hash: `%v`", err)
	}
}

func SaveAvgBuyPriceToRedis(coin string, avgBuyPrice float64, quantity float64) {
	redisClient := GetRedisClient()
	avgPriceKey := fmt.Sprintf(RedisAvgBuyPriceKeyVar, coin)
	err := redisClient.HSet(ctx, avgPriceKey,
		"AvgBuyPrice", avgBuyPrice,
		"TotalQuantity", quantity).Err()
	if err != nil {
		Log(ERROR, "setting sell order hash: `%v`", err)
		return
	}
}
func ReadAvgBuyPriceFromRedis(coin string) (avgBuyPrice float64, quantity float64) {
	redisClient := GetRedisClient()
	avgPrice := 0.0
	totalQuantity := 0.0
	avgPriceKey := fmt.Sprintf(RedisAvgBuyPriceKeyVar, coin)
	result, err := redisClient.HGetAll(ctx, avgPriceKey).Result()
	if err != nil {
		Log(ERROR, "getting avgerage buy price hash from Redis for the coin %v: %v", coin, err)
	} else if len(result) != 0 {
		avgPrice, err = strconv.ParseFloat(result["AvgBuyPrice"], 64)
		if err != nil {
			avgPrice = 0.0
			Log(ERROR, "parsing buy price for avgBuyPrice redis: `%v`", err)

		}
		totalQuantity, err = strconv.ParseFloat(result["TotalQuantity"], 64)
		if err != nil {
			totalQuantity = 0.0
			Log(ERROR, "parsing quantity for avgBuyPrice redis: `%v`", err)
		}
	}
	return avgPrice, totalQuantity
}
