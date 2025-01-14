package api

import (
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

type GenericMap map[string]interface{}

type ApiTradingClient struct {
	secretKey string
	apiKey    string
	baseUrl   string
	headers   map[string]string
}

func NewApiTradingClient(apiKeys common.AccountKeys) *ApiTradingClient {
	return &ApiTradingClient{
		secretKey: apiKeys.SecretKey,
		apiKey:    apiKeys.ApiKey,
		baseUrl:   "https://coinswitch.co",
		headers:   map[string]string{"Content-Type": "application/json"},
	}
}

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

func (apiClient *ApiTradingClient) NewApiTradingClientSingleton() (*ApiTradingClient, error) {
	if apiClient.secretKey == "" {
		return NewApiTradingClientFromConfig(configs.AccountId)
	}
	return apiClient, nil
}

func removeTrailingZeros(dictionary GenericMap) GenericMap {
	for key, value := range dictionary {
		switch v := value.(type) {
		case float64:
			if v == float64(int(v)) {
				dictionary[key] = int(v)
			}
		}
	}
	return dictionary
}

func convertOrderDataToCSXOrder(orderMetadata map[string]interface{}) common.CSXOrder {
	originalQuantity, ok := orderMetadata["orig_qty"].(float64)
	if !ok {
		common.Log(common.WARNING, "Missing or invalid 'orig_qty' in order metadata: %v", orderMetadata)
		originalQuantity = 0
	}

	executedQuantity, ok := orderMetadata["executed_qty"].(float64)
	if !ok {
		common.Log(common.WARNING, "Missing or invalid 'executed_qty' in order metadata: %v", orderMetadata)
		executedQuantity = 0
	}
	remainingQuantity := originalQuantity - executedQuantity

	createdTime, ok := orderMetadata["created_time"].(float64)
	if !ok {
		common.Log(common.WARNING, "Missing or invalid 'created_time' in order metadata: %v", orderMetadata)
		createdTime = 0
	}

	updatedTime, ok := orderMetadata["updated_time"].(float64)
	if !ok {
		common.Log(common.WARNING, "Missing or invalid 'updated_time' in order metadata: %v", orderMetadata)
		updatedTime = 0
	}

	singleOrder := common.CSXOrder{
		Id:                orderMetadata["order_id"].(string),
		Symbol:            orderMetadata["symbol"].(string),
		Status:            orderMetadata["status"].(string),
		Side:              orderMetadata["side"].(string),
		Exchange:          orderMetadata["exchange"].(string),
		Price:             orderMetadata["price"].(float64),
		OriginalQuantity:  originalQuantity,
		ExecutedQuantity:  executedQuantity,
		RemainingQuantity: remainingQuantity,
		CreatedTime:       time.UnixMilli(int64(createdTime)),
		UpdatedTime:       time.UnixMilli(int64(updatedTime)),
	}
	return singleOrder
}

func (client *ApiTradingClient) callApi(url, method string, headers map[string]string, payload GenericMap) (GenericMap, error) {
	/*
		make an API call on webserver and return response

		Args:
		url (str): The API url to be called
		method (str): The API method
		headers (dict): required headers for API call
		payload (dict): payload for API call

		Returns:
		json: The response of the request
	*/
	finalHeaders := make(map[string]string)
	for k, v := range client.headers {
		finalHeaders[k] = v
	}
	for k, v := range headers {
		finalHeaders[k] = v
	}

	var body []byte
	if len(payload) > 0 {
		body, _ = json.Marshal(payload)
	}

	req, err := http.NewRequest(method, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	for k, v := range finalHeaders {
		req.Header.Set(k, v)
	}

	clientResp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer clientResp.Body.Close()

	respBody, err := io.ReadAll(clientResp.Body)
	if err != nil {
		return nil, err
	}

	var respJson map[string]interface{}
	err = json.Unmarshal(respBody, &respJson)
	if err != nil {
		return nil, err
	}

	if clientResp.StatusCode == 429 {
		common.Log(common.INFO, "URL: `%s` : RATE LIMITING", url)
	}

	return respJson, nil
}

func (client *ApiTradingClient) signatureMessage(method, endpoint string, payload GenericMap) (string, error) {
	/*
		Generate signature message to be signed for given request

			Args:
			method (str): The API method
			endpoint (str): The API url to be called
			payload (dict): payload for API call

			Returns:
			json: The signature message for corresponding API call
	*/
	var payloadStr string
	if payload == nil {
		payloadStr = "{}"
	} else {
		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}
		payloadStr = string(payloadBytes)
	}
	return method + endpoint + payloadStr, nil
}

func (client *ApiTradingClient) getSignatureOfRequest(secretKey, requestString string) (string, error) {
	/*
		Returns the signature of the request

			Args:
			secret_key (str): The secret key used to sign the request.
			request_string (str): The string representation of the request.

			Returns:
			str: The signature of the request.
	*/
	requestBytes := []byte(requestString)
	secretKeyBytes, err := hex.DecodeString(secretKey)
	if err != nil {
		return "", err
	}
	if len(secretKeyBytes) != 32 {
		return "", fmt.Errorf("secret key length is not 32 bytes")
	}

	privateKey := ed25519.NewKeyFromSeed(secretKeyBytes)
	signature := ed25519.Sign(privateKey, requestBytes)

	return hex.EncodeToString(signature), nil
}

func (client *ApiTradingClient) makeRequest(method, endpoint string, payload, params GenericMap) (GenericMap, error) {
	/*
		Make the request to :
			a. generate signature message
			b. generate signature signed by secret key
			c. send an API call with the encoded URL

		Args:
			method (str): The method to call API
			endpoint (str): The request endpoint to make API call
			payload (dict): The payload to make API call for POST request
			params (dict): The params to make GET request

		Returns:
			dict: The response of the request.
	*/
	var queryParams string
	if method == "GET" && len(params) > 0 {
		queryParamsList := make([]string, 0, len(params))
		for key, value := range params {
			queryParamsList = append(queryParamsList, fmt.Sprintf("%s=%v", key, value))
		}
		sort.Strings(queryParamsList)
		queryParams = strings.Join(queryParamsList, "&")
	}

	decodedEndpoint := endpoint
	if queryParams != "" {
		decodedEndpoint += "?" + queryParams
	}

	signatureMsg, err := client.signatureMessage(method, decodedEndpoint, payload)
	if err != nil {
		return nil, err
	}
	signature, err := client.getSignatureOfRequest(client.secretKey, signatureMsg)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{
		"X-AUTH-SIGNATURE": signature,
		"X-AUTH-APIKEY":    client.apiKey,
		"Content-Type":     "application/json",
	}

	url := fmt.Sprintf("%s%s", client.baseUrl, decodedEndpoint)

	return client.callApi(url, method, headers, payload)
}
func (client *ApiTradingClient) CheckConnection() (map[string]interface{}, error) {
	return client.makeRequest("GET", "/trade/api/v2/validate/keys", nil, nil)
}
func (client *ApiTradingClient) CheckCoins(params map[string]interface{}) ([]string, error) {

	var emptyValidCoins []string
	jsonResponse, err := client.makeRequest("GET", "/trade/api/v2/coins", nil, params)

	if err != nil {
		common.Log(common.ERROR, "%v", err)
		return emptyValidCoins, err
	}

	validJsonResponse, ok := jsonResponse["data"].(map[string]interface{})
	if !ok {
		errorLog := fmt.Sprintf("Invalid response for '/trade/api/v2/coins' with params: %v\nResponse: %v", params, jsonResponse)
		err = errors.New(errorLog)
		common.Log(common.ERROR, "%v", err)
		return emptyValidCoins, err
	}
	exchange, ok := params["exchange"].(string)
	if !ok {
		errorLog := fmt.Sprintf("Invalid response for '/trade/api/v2/coins' with params: %v\nResponse: %v", params, jsonResponse)
		err = errors.New(errorLog)
		common.Log(common.ERROR, "%v", err)
		return emptyValidCoins, err
	}
	exchangeCoins, ok := validJsonResponse[exchange].([]interface{})
	if !ok {
		errorLog := fmt.Sprintf("Invalid response for '/trade/api/v2/coins' with params: %v\nResponse: %v", params, jsonResponse)
		err = errors.New(errorLog)
		common.Log(common.ERROR, "%v", err)
		return emptyValidCoins, err
	}
	for _, symbol := range exchangeCoins {
		strSymbol, ok := symbol.(string)
		if !ok {
			continue
		}
		emptyValidCoins = append(emptyValidCoins, strSymbol)
	}
	return emptyValidCoins, nil

}

func (client *ApiTradingClient) CreateOrder(payload map[string]interface{}) (map[string]interface{}, error) {
	payload = removeTrailingZeros(payload)
	return client.makeRequest("POST", "/trade/api/v2/order", payload, nil)
}

func (client *ApiTradingClient) CancelOrder(payload map[string]interface{}) (map[string]interface{}, error) {
	return client.makeRequest("DELETE", "/trade/api/v2/order", payload, nil)
}

func (client *ApiTradingClient) GetOrderById(params map[string]interface{}) (common.CSXOrder, error) {
	OrderResponse, err := client.makeRequest("GET", "/trade/api/v2/order", nil, params)
	if err != nil {
		common.Log(common.ERROR, "reading order with params: `%v`, error: `%v`\n", params, err)
		return common.CSXOrder{}, err
	}
	responseMessage, exists := OrderResponse["message"]
	if exists {
		return common.CSXOrder{}, fmt.Errorf("ERROR in reading order with params: `%v`, response message: `%v`", params, responseMessage)
	}
	orderMetadata, ok := OrderResponse["data"].(map[string]interface{})
	if !ok {
		common.Log(common.ERROR, "Invalid or missing order data in response: %v", OrderResponse)
		return common.CSXOrder{}, fmt.Errorf("invalid or missing order data")
	}
	singleOrder := convertOrderDataToCSXOrder(orderMetadata)
	return singleOrder, nil
}

func (client *ApiTradingClient) getOrders(params map[string]interface{}) ([]common.CSXOrder, error) {
	openOrders := []common.CSXOrder{}
	openOrdersResponse, err := client.makeRequest("GET", "/trade/api/v2/orders", nil, params)
	if err != nil {
		common.Log(common.ERROR, "reading open orders with params: `%v`, error: `%v`\n", params, err)
		return openOrders, err
	}
	openOrdersData, ok := openOrdersResponse["data"].(map[string]interface{})
	if !ok {
		return openOrders, fmt.Errorf("invalid or missing 'data' in response: %v", openOrdersResponse)
	}

	orders, ok := openOrdersData["orders"].([]interface{})
	if !ok {
		return openOrders, fmt.Errorf("invalid or missing 'orders' in data: %v", openOrdersData)
	}

	for _, metadata := range orders {
		orderMetadata, ok := metadata.(map[string]interface{})
		if ok {
			openOrders = append(openOrders, convertOrderDataToCSXOrder(orderMetadata))
		}
	}
	return openOrders, nil
}

func (client *ApiTradingClient) GetOpenOrders(params map[string]interface{}) ([]common.CSXOrder, error) {
	params["open"] = true
	return client.getOrders(params)
}

func (client *ApiTradingClient) GetClosedOrders(params map[string]interface{}) ([]common.CSXOrder, error) {
	params["open"] = false
	return client.getOrders(params)
}

func (client *ApiTradingClient) GetUserPortfolio() (map[string]common.PortfolioCoinBalance, error) {
	/*
		Finds the current balance of the user portfolio.
		Returns coin wise holding quantity and total amount based on average_buy_price.
	*/
	portfolioBalance := make(map[string]common.PortfolioCoinBalance)

	userPortfolioResponse, err := client.makeRequest("GET", "/trade/api/v2/user/portfolio", nil, nil)
	if err != nil {
		common.Log(common.ERROR, "fetching user portfolio: `%v`", err)
		return portfolioBalance, err
	}
	// Check if the "data" key exists and is of the expected type
	userPortfolio, ok := userPortfolioResponse["data"].([]interface{})
	if !ok {
		common.Log(common.ERROR, "invalid or missing 'data' in response: `%v`", userPortfolioResponse)
		return portfolioBalance, fmt.Errorf("invalid or missing 'data' in response")
	}

	for _, metadata := range userPortfolio {
		coinMetadata, ok := metadata.(map[string]interface{})
		if !ok {
			common.Log(common.ERROR, "invalid coin metadata: `%v`", metadata)
			continue
		}
		coin, ok := coinMetadata["currency"].(string)
		if !ok {
			common.Log(common.ERROR, "invalid or missing 'currency' in coin metadata: `%v`", coinMetadata)
			continue
		}
		quantityStr, ok := coinMetadata["main_balance"].(string)
		if !ok {
			common.Log(common.ERROR, "invalid or missing 'main_balance' in coin metadata: `%v`", coinMetadata)
			continue
		}
		quantity, err := strconv.ParseFloat(quantityStr, 64)
		if err != nil {
			common.Log(common.ERROR, "converting 'main_balance' to float64 for %s: `%v`", coin, err)
			continue
		}
		var inrBuyAveragePrice float64
		if coin != common.BaseINR {
			inrBuyAveragePriceStr, ok := coinMetadata["buy_average_price"].(string)
			if !ok {
				common.Log(common.ERROR, "invalid or missing 'buy_average_price' in coin metadata: `%v`", coinMetadata)
				continue
			}
			inrBuyAveragePrice, err = strconv.ParseFloat(inrBuyAveragePriceStr, 64)
			if err != nil {
				common.Log(common.ERROR, "converting 'buy_average_price' to float64 for %s: `%v`", coin, err)
				continue
			}
		}

		var holdingInINR float64
		if coin == common.BaseINR {
			holdingInINR = quantity
		} else {
			holdingInINR = quantity * inrBuyAveragePrice
		}
		portfolioBalance[coin] = common.PortfolioCoinBalance{
			Quantity: quantity,
			Amount:   holdingInINR,
		}
	}
	return portfolioBalance, nil
}

func (client *ApiTradingClient) Get24hAllPairsData(params map[string]interface{}) (map[string]interface{}, error) {
	return client.makeRequest("GET", "/trade/api/v2/24hr/all-pairs/ticker", nil, params)
}

func (client *ApiTradingClient) Get24hCoinPairData(params map[string]interface{}) (map[string]interface{}, error) {
	return client.makeRequest("GET", "/trade/api/v2/24hr/ticker", nil, params)
}

func (client *ApiTradingClient) GetDepth(params map[string]interface{}) (map[string]interface{}, error) {
	return client.makeRequest("GET", "/trade/api/v2/depth", nil, params)
}

func (client *ApiTradingClient) GetTrades(params map[string]interface{}) (map[string]interface{}, error) {
	return client.makeRequest("GET", "/trade/api/v2/trades", nil, params)
}

func (client *ApiTradingClient) GetCandlestickData(params map[string]interface{}) ([]common.CandleStickData, error) {
	var candleStickRecords []common.CandleStickData

	symbol, exists := params["symbol"].(string)
	if !exists {
		return candleStickRecords, fmt.Errorf("symbol does not exist in params for fetching candlestick")
	}
	response, err := client.makeRequest("GET", "/trade/api/v2/candles", nil, params)
	if err != nil {
		return candleStickRecords, err
	}
	data, ok := response["data"].([]interface{})
	if !ok || len(data) == 0 {
		return candleStickRecords, fmt.Errorf("no candlestick data found for symbol: %s", symbol)
	}
	for _, entry := range data {
		candle, ok := entry.(map[string]interface{})
		if !ok {
			common.Log(common.INFO, "Skipping entry due to type assertion failure: %v", entry)
			continue
		}

		highestPriceStr, ok := candle["h"].(string)
		if !ok {
			common.Log(common.INFO, "Missing or invalid highestPrice in candle: %v", candle)
			continue
		}
		highestPrice, err := strconv.ParseFloat(highestPriceStr, 64)
		if err != nil {
			common.Log(common.INFO, "Error converting highestPrice to float64: %v", err)
			continue
		}

		closePriceStr, ok := candle["c"].(string)
		if !ok {
			common.Log(common.INFO, "Missing or invalid closePrice in candle: %v", candle)
			continue
		}
		closePrice, err := strconv.ParseFloat(closePriceStr, 64)
		if err != nil {
			common.Log(common.INFO, "Error converting closePrice to float64: %v", err)
			continue
		}

		openPriceStr, ok := candle["o"].(string)
		if !ok {
			common.Log(common.INFO, "Missing or invalid openPrice in candle: %v", candle)
			continue
		}
		openPrice, err := strconv.ParseFloat(openPriceStr, 64)
		if err != nil {
			common.Log(common.INFO, "Error converting openPrice to float64: %v", err)
			continue
		}

		lowestPriceStr, ok := candle["l"].(string)
		if !ok {
			common.Log(common.INFO, "Missing or invalid lowestPrice in candle: %v", candle)
			continue
		}
		lowestPrice, err := strconv.ParseFloat(lowestPriceStr, 64)
		if err != nil {
			common.Log(common.INFO, "Error converting lowestPrice to float64: %v", err)
			continue
		}

		volumeStr, ok := candle["volume"].(string)
		if !ok {
			common.Log(common.INFO, "Missing or invalid volume in candle: %v", candle)
			continue
		}
		volume, err := strconv.ParseFloat(volumeStr, 64)
		if err != nil {
			common.Log(common.INFO, "Error converting volume to float64: %v", err)
			continue
		}

		startTimeStr, ok := candle["start_time"].(string)
		if !ok {
			common.Log(common.WARNING, "Missing or invalid 'start_time' in candle: %v", err)
			continue
		}
		startTime, err := strconv.ParseFloat(startTimeStr, 64)
		if err != nil {
			common.Log(common.INFO, "Error converting startTime to float64: %v", err)
			continue
		}

		closeTimeStr, ok := candle["close_time"].(string)
		if !ok {
			common.Log(common.WARNING, "Missing or invalid 'close_time' in candle: %v", err)
			continue
		}
		closeTime, err := strconv.ParseFloat(closeTimeStr, 64)
		if err != nil {
			common.Log(common.INFO, "Error converting closeTime to float64: %v", err)
			continue
		}

		intervalStr, ok := candle["interval"].(string)
		if !ok {
			common.Log(common.WARNING, "Missing or invalid 'interval' in candle: %v", err)
			continue
		}
		interval, err := strconv.ParseFloat(intervalStr, 64)
		if err != nil {
			common.Log(common.INFO, "Error converting interval to float64: %v", err)
			continue
		}

		candleStickRecords = append(candleStickRecords, common.CandleStickData{
			Symbol:    symbol,
			Open:      openPrice,
			High:      highestPrice,
			Low:       lowestPrice,
			Close:     closePrice,
			Interval:  interval,
			Volume:    volume,
			StartTime: time.UnixMilli(int64(startTime)),
			CloseTime: time.UnixMilli(int64(closeTime)),
		})
	}
	return candleStickRecords, nil
}

func (client *ApiTradingClient) GetExchangePrecision(payload map[string]interface{}) (map[string]interface{}, error) {
	return client.makeRequest("POST", "/trade/api/v2/exchangePrecision", payload, nil)
}

func (client *ApiTradingClient) GetBlackistedCoins() []string {
	var coins []string
	response, err := client.makeRequest("GET", "/trade/api/v2/getBlacklistedCoins", nil, nil)
	if err != nil {
		common.Log(common.ERROR, "Getting blacklisted coins: `%v`", err)
		return coins
	}
	coinResp, ok := response["data"].([]string)
	if !ok {
		common.Log(common.ERROR, "Unexpected data format received for blacklisted coins info : `%v`", response)
		return coins
	}
	return coinResp
}

func (client *ApiTradingClient) GetTradingFee() (map[string]float64, error) {
	defaultTradingFee := make(map[string]float64)
	for _, exchange := range common.AllExchanges {
		params := GenericMap{
			"exchange": exchange,
		}

		response, err := client.makeRequest("GET", "/trade/api/v2/tradingFee", nil, params)
		if err != nil {
			common.Log(common.WARNING, "Failed to fetch trading fee for exchange '%s': %v", exchange, err)
			continue
		}

		data, ok := response["data"].(map[string]interface{})
		if !ok {
			common.Log(common.WARNING, "Unexpected data format received for exchange '%s'. Expected map[string]interface{} but got %T", exchange, response["data"])
			continue
		}

		exchangeFees, ok := data[exchange].(map[string]interface{})
		if !ok {
			common.Log(common.WARNING, "Unexpected format for exchange '%s'. Expected map[string]interface{} but got %T", exchange, data[exchange])
			continue
		}
		maxExchangeCommission := 0.0
		for coin, feeData := range exchangeFees {
			coinData, ok := feeData.(map[string]interface{})
			if !ok {
				common.Log(common.WARNING, "Unexpected fee data format for coin '%s' in exchange '%s'. Expected map[string]interface{} but got %T", coin, exchange, feeData)
				continue
			}

			takerFeeAfterDiscount, ok := coinData["taker_fee_after_discount"].(float64)
			if !ok {
				common.Log(common.WARNING, "Missing or invalid 'taker_fee_after_discount' for coin '%s' in exchange '%s'. Expected float64 but got %T", coin, exchange, coinData["taker_fee_after_discount"])
				continue
			}
			if takerFeeAfterDiscount > maxExchangeCommission {
				maxExchangeCommission = takerFeeAfterDiscount
			}

			defaultTradingFee[exchange] = maxExchangeCommission
		}
	}
	return defaultTradingFee, nil

}

func (client *ApiTradingClient) GetTDSInfo() (float64, error) {
	response, err := client.makeRequest("GET", "/trade/api/v2/tds", nil, nil)
	if err != nil {
		common.Log(common.ERROR, "fetching TDS info: `%v`", err)
		return 0.0, err
	}
	data, ok := response["data"].(map[string]interface{})
	if !ok {
		common.Log(common.ERROR, "Unexpected data format received for tds info : `%v`", response)
		return 0.0, fmt.Errorf("unexpected data format received for tds info : `%v`", response)
	}
	TDSAmount, ok := data["total_tds_amount"].(float64)
	if !ok {
		common.Log(common.WARNING, "Missing or invalid 'total_tds_amount' : `%v`", data)
		return 0.0, fmt.Errorf("missing or invalid 'total_tds_amount' : `%v`", data)
	}
	return TDSAmount, nil
}

func (client *ApiTradingClient) GetFinancialInfo() (common.FinancialInfo, error) {
	var financialInfo common.FinancialInfo

	response, err := client.makeRequest("GET", "/trade/api/v2/financialInfo", nil, nil)
	if err != nil {
		common.Log(common.ERROR, "fetching TDS info: `%v`", err)
		return financialInfo, err
	}
	data, ok := response["data"].(map[string]interface{})
	if !ok {
		common.Log(common.ERROR, "Unexpected data format received for financial info. Expected map[string]interface{} : `%v`", response)
		return financialInfo, fmt.Errorf("unexpected data format received for financial info. Expected map[string]interface{} : `%v`", response)
	}
	investedValue, ok := data["invested_value"].(string)
	if !ok {
		common.Log(common.WARNING, "Missing or invalid 'invested_value' : `%v`", data)
		return financialInfo, fmt.Errorf("missing or invalid 'invested_value': `%v`", data)
	}
	financialInfo.InvestedValue, _ = strconv.ParseFloat(investedValue, 64)

	currentValue, ok := data["current_value"].(string)
	if !ok {
		common.Log(common.WARNING, "Missing or invalid 'current_value' : `%v`", data)
		return financialInfo, fmt.Errorf("missing or invalid 'current_value' : `%v`", data)
	}
	financialInfo.CurrentValue, _ = strconv.ParseFloat(currentValue, 64)

	pnl, ok := data["pnl"].(string)
	if !ok {
		common.Log(common.WARNING, "Missing or invalid 'pnl' : `%v`", data)
		return financialInfo, fmt.Errorf("missing or invalid 'pnl' : `%v`", data)
	}
	financialInfo.PnL, _ = strconv.ParseFloat(pnl, 64)

	tdsMetadata, ok := data["tds"].(map[string]interface{})
	if !ok {
		common.Log(common.WARNING, "Missing or invalid 'tds' : `%v`", data)
		return financialInfo, fmt.Errorf("missing or invalid 'tds' : `%v`", data)
	}
	totalTDS, ok := tdsMetadata["total_tds"].(string)
	if !ok {
		common.Log(common.WARNING, "Missing or invalid 'total_tds' : `%v`", data)
		return financialInfo, fmt.Errorf("missing or invalid 'total_tds' : `%v`", data)
	}
	financialInfo.TotalTDS, _ = strconv.ParseFloat(totalTDS, 64)

	return financialInfo, nil
}
