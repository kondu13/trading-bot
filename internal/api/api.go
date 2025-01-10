package api

import(
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"trade_bot/internal/common"
	"trade_bot/internal/configs"
)

type GenericMap map[string] interface{}

type ApiTradingClient struct{
	secretKey string
	apiKey string
	baseUrl string
	headers map[string]string
}

// creates an instance
func NewApiTradingClient (apiKeys common.AccountKeys) *ApiTradingClient{
	return &ApiTradingClient{
		secretKey: apiKeys.SecretKey,
		apiKey: apiKeys.ApiKey,
		baseUrl: "https://coinswitch.co",
		headers: map[string]string{"Content-Type" : "application/json"},
	}
}

// Reads the values from the json file and passes to the above function to create an instance
func NewApiTradingClientFromConfig(accountId string) (*ApiTradingClient, error) {
	absPath, _ := filepath.Abs(common.ApiKeysJsonPath)
	jsonFile, err := os.Open(absPath)
	if err != nil {
		common.Log(common.ERROR, "reading the file: %s.\n%s", absPath, err)
		return nil, err
	}
	decoder := json.NewDecoder(jsonFile)
	var allAccountKeys map[string]common.AccountKeys
	err = decoder.Decode(&allAccountKeys)
	if err != nil {
		common.Log(common.ERROR, "%v", err)
		return nil, err
	}
	return NewApiTradingClient(allAccountKeys[accountId]), nil
}

// Ensures only one instance is running
func (apiClient *ApiTradingClient) NewApiTradingClientSingleton() (*ApiTradingClient, error) {
	if apiClient.secretKey == "" {
		return NewApiTradingClientFromConfig(configs.AccountId)
	}
	return apiClient, nil
}

