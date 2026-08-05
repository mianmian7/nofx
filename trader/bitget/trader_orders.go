package bitget

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"nofx/logger"
	"nofx/trader/types"
	"strconv"
	"strings"
)

// normalizeTakeProfitTriggerPrice aligns an absolute Bitget trigger to the
// contract's decimal price grid without moving the target farther away.
func normalizeTakeProfitTriggerPrice(price float64, pricePlace int, positionSide string) float64 {
	if price <= 0 || pricePlace < 0 {
		return price
	}
	scale := math.Pow10(pricePlace)
	if strings.EqualFold(positionSide, "SHORT") {
		return math.Ceil(price*scale-1e-9) / scale
	}
	return math.Floor(price*scale+1e-9) / scale
}

// normalizeStopLossTriggerPrice rounds an absolute Bitget stop toward the
// current market so tick conversion cannot loosen protection: long stops ceil,
// short stops floor. The PnL-to-price conversion happens before this helper.
func normalizeStopLossTriggerPrice(price float64, pricePlace int, positionSide string) float64 {
	if price <= 0 || pricePlace < 0 {
		return price
	}
	scale := math.Pow10(pricePlace)
	if strings.EqualFold(positionSide, "SHORT") {
		return math.Floor(price*scale+1e-9) / scale
	}
	return math.Ceil(price*scale-1e-9) / scale
}

// OpenLong opens long position
func (t *BitgetTrader) OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	symbol = t.convertSymbol(symbol)

	// Cancel old orders first
	if err := t.CancelAllOrders(symbol); err != nil {
		logger.Infof("  ⚠ Failed to cancel old pending orders (may not have any): %v", err)
	}

	// Set leverage
	if err := t.SetLeverage(symbol, leverage); err != nil {
		logger.Infof("  ⚠️ Failed to set leverage: %v", err)
	}

	// Format quantity
	qtyStr, _ := t.FormatQuantity(symbol, quantity)

	body := map[string]interface{}{
		"symbol":      symbol,
		"productType": "USDT-FUTURES",
		"marginMode":  "crossed",
		"marginCoin":  "USDT",
		"side":        "buy",
		"tradeSide":   "open",
		"orderType":   "market",
		"size":        qtyStr,
		"clientOid":   genBitgetClientOid(),
	}

	logger.Infof("  📊 Bitget OpenLong: symbol=%s, qty=%s, leverage=%d", symbol, qtyStr, leverage)

	data, err := t.doRequest("POST", bitgetOrderPath, body)
	if err != nil {
		return nil, fmt.Errorf("failed to open long position: %w", err)
	}

	var order struct {
		OrderId   string `json:"orderId"`
		ClientOid string `json:"clientOid"`
	}

	if err := json.Unmarshal(data, &order); err != nil {
		return nil, fmt.Errorf("failed to parse order response: %w", err)
	}

	// Clear cache
	t.clearCache()

	logger.Infof("✓ Bitget opened long position successfully: %s", symbol)

	return map[string]interface{}{
		"orderId": order.OrderId,
		"symbol":  symbol,
		"status":  "FILLED",
	}, nil
}

// OpenShort opens short position
func (t *BitgetTrader) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	symbol = t.convertSymbol(symbol)

	// Cancel old orders first
	if err := t.CancelAllOrders(symbol); err != nil {
		logger.Infof("  ⚠ Failed to cancel old pending orders (may not have any): %v", err)
	}

	// Set leverage
	if err := t.SetLeverage(symbol, leverage); err != nil {
		logger.Infof("  ⚠️ Failed to set leverage: %v", err)
	}

	// Format quantity
	qtyStr, _ := t.FormatQuantity(symbol, quantity)

	body := map[string]interface{}{
		"symbol":      symbol,
		"productType": "USDT-FUTURES",
		"marginMode":  "crossed",
		"marginCoin":  "USDT",
		"side":        "sell",
		"tradeSide":   "open",
		"orderType":   "market",
		"size":        qtyStr,
		"clientOid":   genBitgetClientOid(),
	}

	logger.Infof("  📊 Bitget OpenShort: symbol=%s, qty=%s, leverage=%d", symbol, qtyStr, leverage)

	data, err := t.doRequest("POST", bitgetOrderPath, body)
	if err != nil {
		return nil, fmt.Errorf("failed to open short position: %w", err)
	}

	var order struct {
		OrderId   string `json:"orderId"`
		ClientOid string `json:"clientOid"`
	}

	if err := json.Unmarshal(data, &order); err != nil {
		return nil, fmt.Errorf("failed to parse order response: %w", err)
	}

	// Clear cache
	t.clearCache()

	logger.Infof("✓ Bitget opened short position successfully: %s", symbol)

	return map[string]interface{}{
		"orderId": order.OrderId,
		"symbol":  symbol,
		"status":  "FILLED",
	}, nil
}

// CloseLong closes long position
func (t *BitgetTrader) CloseLong(symbol string, quantity float64) (map[string]interface{}, error) {
	symbol = t.convertSymbol(symbol)

	// If quantity is 0, get current position
	if quantity == 0 {
		positions, err := t.GetPositions()
		if err != nil {
			return nil, err
		}
		for _, pos := range positions {
			if pos["symbol"] == symbol && pos["side"] == "long" {
				quantity = pos["positionAmt"].(float64)
				break
			}
		}
		if quantity == 0 {
			return nil, fmt.Errorf("long position not found for %s", symbol)
		}
	}

	// Format quantity
	qtyStr, _ := t.FormatQuantity(symbol, quantity)

	body := map[string]interface{}{
		"symbol":      symbol,
		"productType": "USDT-FUTURES",
		"marginMode":  "crossed",
		"marginCoin":  "USDT",
		"side":        "buy",
		"tradeSide":   "close",
		"orderType":   "market",
		"size":        qtyStr,
		"clientOid":   genBitgetClientOid(),
	}

	logger.Infof("  📊 Bitget CloseLong: symbol=%s, qty=%s", symbol, qtyStr)

	data, err := t.doRequest("POST", bitgetOrderPath, body)
	if err != nil {
		return nil, fmt.Errorf("failed to close long position: %w", err)
	}

	var order struct {
		OrderId string `json:"orderId"`
	}

	if err := json.Unmarshal(data, &order); err != nil {
		return nil, err
	}

	// Clear cache
	t.clearCache()

	logger.Infof("✓ Bitget closed long position successfully: %s", symbol)

	return map[string]interface{}{
		"orderId": order.OrderId,
		"symbol":  symbol,
		"status":  "FILLED",
	}, nil
}

// CloseShort closes short position
func (t *BitgetTrader) CloseShort(symbol string, quantity float64) (map[string]interface{}, error) {
	symbol = t.convertSymbol(symbol)

	// If quantity is 0, get current position
	if quantity == 0 {
		positions, err := t.GetPositions()
		if err != nil {
			return nil, err
		}
		for _, pos := range positions {
			if pos["symbol"] == symbol && pos["side"] == "short" {
				quantity = pos["positionAmt"].(float64)
				break
			}
		}
		if quantity == 0 {
			return nil, fmt.Errorf("short position not found for %s", symbol)
		}
	}

	// Ensure quantity is positive
	if quantity < 0 {
		quantity = -quantity
	}

	// Format quantity
	qtyStr, _ := t.FormatQuantity(symbol, quantity)

	body := map[string]interface{}{
		"symbol":      symbol,
		"productType": "USDT-FUTURES",
		"marginMode":  "crossed",
		"marginCoin":  "USDT",
		"side":        "sell",
		"tradeSide":   "close",
		"orderType":   "market",
		"size":        qtyStr,
		"clientOid":   genBitgetClientOid(),
	}

	logger.Infof("  📊 Bitget CloseShort: symbol=%s, qty=%s", symbol, qtyStr)

	data, err := t.doRequest("POST", bitgetOrderPath, body)
	if err != nil {
		return nil, fmt.Errorf("failed to close short position: %w", err)
	}

	var order struct {
		OrderId string `json:"orderId"`
	}

	if err := json.Unmarshal(data, &order); err != nil {
		return nil, err
	}

	// Clear cache
	t.clearCache()

	logger.Infof("✓ Bitget closed short position successfully: %s", symbol)

	return map[string]interface{}{
		"orderId": order.OrderId,
		"symbol":  symbol,
		"status":  "FILLED",
	}, nil
}

// SetStopLoss sets stop loss order
func (t *BitgetTrader) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
	// Bitget V2 uses the dedicated TPSL endpoint for position stop-loss orders.
	// The generic place-plan-order endpoint only accepts normal_plan/track_plan;
	// sending loss_plan there returns 400172 (planType Illegal type).
	if stopPrice <= 0 || math.IsNaN(stopPrice) || math.IsInf(stopPrice, 0) {
		return fmt.Errorf("stop-loss trigger price must be positive and finite")
	}
	symbol = t.convertSymbol(symbol)
	contract, err := t.getContract(symbol)
	if err != nil {
		return fmt.Errorf("failed to get contract precision for stop loss: %w", err)
	}
	stopPrice = normalizeStopLossTriggerPrice(stopPrice, contract.PricePlace, positionSide)

	// This trader explicitly configures Bitget hedge mode. In that mode
	// holdSide identifies the protected long/short position.
	holdSide := "long"
	if strings.ToUpper(positionSide) == "SHORT" {
		holdSide = "short"
	}

	qtyStr, err := t.FormatQuantity(symbol, quantity)
	if err != nil {
		return fmt.Errorf("failed to format stop-loss quantity: %w", err)
	}

	body := map[string]interface{}{
		"planType":     "loss_plan",
		"symbol":       symbol,
		"productType":  "USDT-FUTURES",
		"marginCoin":   "USDT",
		"triggerPrice": fmt.Sprintf("%.8f", stopPrice),
		"triggerType":  "mark_price",
		"executePrice": "0", // market execution
		"holdSide":     holdSide,
		"size":         qtyStr,
		"clientOid":    genBitgetClientOid(),
	}

	_, err = t.doRequest("POST", bitgetTPSLOrderPath, body)
	if err != nil {
		return fmt.Errorf("failed to set stop loss: %w", err)
	}

	logger.Infof("  ✓ [Bitget] Stop loss set: %s @ %.4f", symbol, stopPrice)
	return nil
}

// SetTakeProfit sets take profit order
func (t *BitgetTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	// Bitget V2 uses the dedicated TPSL endpoint for position take-profit orders.
	if takeProfitPrice <= 0 || math.IsNaN(takeProfitPrice) || math.IsInf(takeProfitPrice, 0) {
		return fmt.Errorf("take-profit trigger price must be positive and finite")
	}
	symbol = t.convertSymbol(symbol)
	contract, err := t.getContract(symbol)
	if err != nil {
		return fmt.Errorf("failed to get contract precision for take profit: %w", err)
	}
	takeProfitPrice = normalizeTakeProfitTriggerPrice(takeProfitPrice, contract.PricePlace, positionSide)

	// This trader explicitly configures Bitget hedge mode. In that mode
	// holdSide identifies the protected long/short position.
	holdSide := "long"
	if strings.ToUpper(positionSide) == "SHORT" {
		holdSide = "short"
	}

	qtyStr, err := t.FormatQuantity(symbol, quantity)
	if err != nil {
		return fmt.Errorf("failed to format take-profit quantity: %w", err)
	}

	body := map[string]interface{}{
		"planType":     "profit_plan",
		"symbol":       symbol,
		"productType":  "USDT-FUTURES",
		"marginCoin":   "USDT",
		"triggerPrice": fmt.Sprintf("%.8f", takeProfitPrice),
		"triggerType":  "mark_price",
		"executePrice": "0", // market execution
		"holdSide":     holdSide,
		"size":         qtyStr,
		"clientOid":    genBitgetClientOid(),
	}

	_, err = t.doRequest("POST", bitgetTPSLOrderPath, body)
	if err != nil {
		return fmt.Errorf("failed to set take profit: %w", err)
	}

	logger.Infof("  ✓ [Bitget] Take profit set: %s @ %.4f", symbol, takeProfitPrice)
	return nil
}

// GetProtectionPriceTick exposes the exact trigger-price grid used by the
// Bitget TPSL endpoints. PricePlace is decimal precision, not a fee rate.
func (t *BitgetTrader) GetProtectionPriceTick(symbol string) (float64, error) {
	contract, err := t.getContract(t.convertSymbol(symbol))
	if err != nil {
		return 0, err
	}
	if contract.PricePlace < 0 {
		return 0, fmt.Errorf("invalid Bitget price precision %d for %s", contract.PricePlace, symbol)
	}
	return math.Pow10(-contract.PricePlace), nil
}

// ModifyProtectionOrder updates an existing TPSL trigger atomically through
// Bitget's modify-tpsl endpoint, avoiding a cancel-then-create protection gap.
func (t *BitgetTrader) ModifyProtectionOrder(symbol, orderID, kind, positionSide string, quantity, triggerPrice float64) error {
	if strings.TrimSpace(orderID) == "" {
		return fmt.Errorf("Bitget TPSL modification requires an order ID")
	}
	if triggerPrice <= 0 || math.IsNaN(triggerPrice) || math.IsInf(triggerPrice, 0) {
		return fmt.Errorf("Bitget TPSL modification requires a positive finite trigger price")
	}
	symbol = t.convertSymbol(symbol)
	contract, err := t.getContract(symbol)
	if err != nil {
		return fmt.Errorf("failed to get contract precision for TPSL modification: %w", err)
	}
	switch kind {
	case "stop_loss":
		triggerPrice = normalizeStopLossTriggerPrice(triggerPrice, contract.PricePlace, positionSide)
	case "take_profit":
		triggerPrice = normalizeTakeProfitTriggerPrice(triggerPrice, contract.PricePlace, positionSide)
	default:
		return fmt.Errorf("unsupported Bitget TPSL protection kind %q", kind)
	}
	qtyStr, err := t.FormatQuantity(symbol, quantity)
	if err != nil {
		return fmt.Errorf("format Bitget TPSL quantity: %w", err)
	}
	body := map[string]interface{}{
		"orderId":      orderID,
		"symbol":       symbol,
		"productType":  "USDT-FUTURES",
		"marginCoin":   "USDT",
		"triggerPrice": fmt.Sprintf("%.8f", triggerPrice),
		"triggerType":  "mark_price",
		"executePrice": "0",
		"size":         qtyStr,
	}
	if _, err := t.doRequest("POST", bitgetModifyTPSLPath, body); err != nil {
		return fmt.Errorf("modify Bitget %s order %s: %w", kind, orderID, err)
	}
	return nil
}

// CancelStopLossOrders cancels stop loss orders
func (t *BitgetTrader) CancelStopLossOrders(symbol string) error {
	return t.cancelPlanOrders(symbol, "loss_plan")
}

// CancelTakeProfitOrders cancels take profit orders
func (t *BitgetTrader) CancelTakeProfitOrders(symbol string) error {
	return t.cancelPlanOrders(symbol, "profit_plan")
}

// cancelPlanOrders cancels plan orders
func (t *BitgetTrader) cancelPlanOrders(symbol string, planType string) error {
	symbol = t.convertSymbol(symbol)
	orders, err := t.getPendingPlanOrders(symbol)
	if err != nil {
		return fmt.Errorf("query Bitget TPSL orders before cancellation: %w", err)
	}

	orderIDsByPlanType := make(map[string][]string)
	for _, order := range orders {
		if planType == "loss_plan" && order.PlanType != "loss_plan" && order.PlanType != "pos_loss" {
			continue
		}
		if planType == "profit_plan" && order.PlanType != "profit_plan" && order.PlanType != "pos_profit" {
			continue
		}
		if order.OrderID == "" {
			return fmt.Errorf("cannot cancel Bitget %s order with an empty order ID", order.PlanType)
		}
		orderIDsByPlanType[order.PlanType] = append(orderIDsByPlanType[order.PlanType], order.OrderID)
	}

	for originalPlanType, orderIDs := range orderIDsByPlanType {
		orderIDList := make([]map[string]string, 0, len(orderIDs))
		for _, orderID := range orderIDs {
			orderIDList = append(orderIDList, map[string]string{"orderId": orderID})
		}
		body := map[string]interface{}{
			"orderIdList": orderIDList,
			"symbol":      symbol,
			"productType": "USDT-FUTURES",
			"marginCoin":  "USDT",
			"planType":    originalPlanType,
		}
		data, cancelErr := t.doRequest("POST", bitgetCancelPlanPath, body)
		if cancelErr != nil {
			return fmt.Errorf("cancel Bitget %s orders: %w", originalPlanType, cancelErr)
		}
		if err := validateBitgetCancelPlanResult(data, orderIDs); err != nil {
			return fmt.Errorf("cancel Bitget %s orders: %w", originalPlanType, err)
		}
	}
	return nil
}

func validateBitgetCancelPlanResult(data []byte, requestedOrderIDs []string) error {
	var result struct {
		SuccessList []struct {
			OrderID string `json:"orderId"`
		} `json:"successList"`
		FailureList []struct {
			OrderID   string `json:"orderId"`
			ErrorCode string `json:"errorCode"`
			ErrorMsg  string `json:"errorMsg"`
		} `json:"failureList"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("parse cancellation result: %w", err)
	}
	if len(result.FailureList) > 0 {
		failure := result.FailureList[0]
		return fmt.Errorf("order %s failed: code=%s msg=%s", failure.OrderID, failure.ErrorCode, failure.ErrorMsg)
	}
	succeeded := make(map[string]bool, len(result.SuccessList))
	for _, item := range result.SuccessList {
		succeeded[item.OrderID] = true
	}
	for _, orderID := range requestedOrderIDs {
		if !succeeded[orderID] {
			return fmt.Errorf("order %s was not confirmed in successList", orderID)
		}
	}
	return nil
}

// CancelAllOrders cancels all pending orders
func (t *BitgetTrader) CancelAllOrders(symbol string) error {
	symbol = t.convertSymbol(symbol)

	// Get pending orders
	params := map[string]interface{}{
		"symbol":      symbol,
		"productType": "USDT-FUTURES",
	}

	data, err := t.doRequest("GET", bitgetPendingPath, params)
	if err != nil {
		return err
	}

	var orders struct {
		EntrustedList []struct {
			OrderId string `json:"orderId"`
		} `json:"entrustedList"`
	}

	if err := json.Unmarshal(data, &orders); err != nil {
		return err
	}

	// Cancel each order
	for _, order := range orders.EntrustedList {
		body := map[string]interface{}{
			"symbol":      symbol,
			"productType": "USDT-FUTURES",
			"marginCoin":  "USDT",
			"orderId":     order.OrderId,
		}
		if _, cancelErr := t.doRequest("POST", bitgetCancelOrderPath, body); cancelErr != nil {
			return fmt.Errorf("cancel Bitget regular order %s: %w", order.OrderId, cancelErr)
		}
	}

	// Also cancel plan orders
	return errors.Join(
		t.cancelPlanOrders(symbol, "loss_plan"),
		t.cancelPlanOrders(symbol, "profit_plan"),
	)
}

// CancelStopOrders cancels stop loss and take profit orders
func (t *BitgetTrader) CancelStopOrders(symbol string) error {
	return errors.Join(
		t.CancelStopLossOrders(symbol),
		t.CancelTakeProfitOrders(symbol),
	)
}

// GetOrderStatus gets order status
func (t *BitgetTrader) GetOrderStatus(symbol string, orderID string) (map[string]interface{}, error) {
	symbol = t.convertSymbol(symbol)

	params := map[string]interface{}{
		"symbol":      symbol,
		"productType": "USDT-FUTURES",
		"orderId":     orderID,
	}

	data, err := t.doRequest("GET", "/api/v2/mix/order/detail", params)
	if err != nil {
		return nil, fmt.Errorf("failed to get order status: %w", err)
	}

	var order struct {
		OrderId    string `json:"orderId"`
		State      string `json:"state"`      // filled, canceled, partially_filled, new
		PriceAvg   string `json:"priceAvg"`   // Average fill price
		BaseVolume string `json:"baseVolume"` // Filled quantity
		Fee        string `json:"fee"`        // Fee
		Side       string `json:"side"`
		OrderType  string `json:"orderType"`
		CTime      string `json:"cTime"`
		UTime      string `json:"uTime"`
	}

	if err := json.Unmarshal(data, &order); err != nil {
		return nil, err
	}

	avgPrice, _ := strconv.ParseFloat(order.PriceAvg, 64)
	fillQty, _ := strconv.ParseFloat(order.BaseVolume, 64)
	fee, _ := strconv.ParseFloat(order.Fee, 64)
	cTime, _ := strconv.ParseInt(order.CTime, 10, 64)
	uTime, _ := strconv.ParseInt(order.UTime, 10, 64)

	// Status mapping
	statusMap := map[string]string{
		"filled":           "FILLED",
		"new":              "NEW",
		"partially_filled": "PARTIALLY_FILLED",
		"canceled":         "CANCELED",
	}

	status := statusMap[order.State]
	if status == "" {
		status = order.State
	}

	return map[string]interface{}{
		"orderId":     order.OrderId,
		"symbol":      symbol,
		"status":      status,
		"avgPrice":    avgPrice,
		"executedQty": fillQty,
		"side":        order.Side,
		"type":        order.OrderType,
		"time":        cTime,
		"updateTime":  uTime,
		"commission":  -fee,
	}, nil
}

type bitgetPendingPlanOrder struct {
	OrderID                 string `json:"orderId"`
	Symbol                  string `json:"symbol"`
	Side                    string `json:"side"`
	PosSide                 string `json:"posSide"`
	HoldSide                string `json:"holdSide"`
	PlanType                string `json:"planType"`
	TriggerPrice            string `json:"triggerPrice"`
	StopLossTriggerPrice    string `json:"stopLossTriggerPrice"`
	StopSurplusTriggerPrice string `json:"stopSurplusTriggerPrice"`
	Size                    string `json:"size"`
	PlanStatus              string `json:"planStatus"`
}

func bitgetProtectionOrderType(planType string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(planType)) {
	case "profit_plan", "pos_profit":
		return "TAKE_PROFIT_MARKET", true
	case "loss_plan", "pos_loss":
		return "STOP_MARKET", true
	default:
		return "", false
	}
}

func parsePositiveBitgetPrice(raw, field, orderID string) (float64, error) {
	price, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return 0, fmt.Errorf("Bitget TPSL order %s has invalid %s %q", orderID, field, raw)
	}
	return price, nil
}

func (t *BitgetTrader) getPendingPlanOrderRecords(symbol string) ([]bitgetPendingPlanOrder, error) {
	symbol = t.convertSymbol(symbol)
	params := map[string]interface{}{
		"symbol":      symbol,
		"productType": "USDT-FUTURES",
		"planType":    "profit_loss",
	}
	data, err := t.doRequest("GET", bitgetPendingPlanPath, params)
	if err != nil {
		return nil, fmt.Errorf("get Bitget pending TPSL orders: %w", err)
	}
	var payload struct {
		EntrustedList []bitgetPendingPlanOrder `json:"entrustedList"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse Bitget pending TPSL orders: %w", err)
	}
	return payload.EntrustedList, nil
}

func parseBitgetPendingPlanOrder(order bitgetPendingPlanOrder) (types.OpenOrder, string, error) {
	orderType, supported := bitgetProtectionOrderType(order.PlanType)
	if !supported {
		return types.OpenOrder{}, "", fmt.Errorf("Bitget TPSL order %s has unsupported planType %q", order.OrderID, order.PlanType)
	}
	triggerField, triggerRaw := "triggerPrice", order.TriggerPrice
	if orderType == "TAKE_PROFIT_MARKET" && strings.TrimSpace(order.StopSurplusTriggerPrice) != "" {
		triggerField, triggerRaw = "stopSurplusTriggerPrice", order.StopSurplusTriggerPrice
	}
	if orderType == "STOP_MARKET" && strings.TrimSpace(order.StopLossTriggerPrice) != "" {
		triggerField, triggerRaw = "stopLossTriggerPrice", order.StopLossTriggerPrice
	}
	triggerPrice, err := parsePositiveBitgetPrice(triggerRaw, triggerField, order.OrderID)
	if err != nil {
		return types.OpenOrder{}, orderType, err
	}
	quantity := 0.0
	if strings.TrimSpace(order.Size) != "" {
		quantity, err = strconv.ParseFloat(order.Size, 64)
		if err != nil || quantity < 0 || math.IsNaN(quantity) || math.IsInf(quantity, 0) {
			return types.OpenOrder{}, orderType, fmt.Errorf("Bitget TPSL order %s has invalid size %q", order.OrderID, order.Size)
		}
	}
	positionSide := order.PosSide
	if positionSide == "" {
		positionSide = order.HoldSide
	}
	status := strings.ToUpper(order.PlanStatus)
	if status == "" {
		status = "NEW"
	}
	return types.OpenOrder{
		OrderID:      order.OrderID,
		Symbol:       order.Symbol,
		Side:         strings.ToUpper(order.Side),
		PositionSide: strings.ToUpper(positionSide),
		Type:         orderType,
		PlanType:     strings.ToLower(order.PlanType),
		StopPrice:    triggerPrice,
		Quantity:     quantity,
		Status:       status,
	}, orderType, nil
}

func (t *BitgetTrader) getPendingPlanOrders(symbol string) ([]types.OpenOrder, error) {
	symbol = t.convertSymbol(symbol)
	records, err := t.getPendingPlanOrderRecords(symbol)
	if err != nil {
		return nil, err
	}
	result := make([]types.OpenOrder, 0, len(records))
	for _, order := range records {
		if !strings.EqualFold(order.Symbol, symbol) {
			continue
		}
		parsed, _, parseErr := parseBitgetPendingPlanOrder(order)
		if parseErr != nil {
			return nil, parseErr
		}
		result = append(result, parsed)
	}
	return result, nil
}

func (t *BitgetTrader) getPendingRegularOrders(symbol string) ([]types.OpenOrder, error) {
	symbol = t.convertSymbol(symbol)
	data, err := t.doRequest("GET", bitgetPendingPath, map[string]interface{}{
		"symbol": symbol, "productType": "USDT-FUTURES",
	})
	if err != nil {
		return nil, fmt.Errorf("get Bitget pending regular orders: %w", err)
	}
	var payload struct {
		EntrustedList []struct {
			OrderID   string `json:"orderId"`
			Symbol    string `json:"symbol"`
			Side      string `json:"side"`
			PosSide   string `json:"posSide"`
			OrderType string `json:"orderType"`
			Price     string `json:"price"`
			Size      string `json:"size"`
			State     string `json:"state"`
		} `json:"entrustedList"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse Bitget pending regular orders: %w", err)
	}
	result := make([]types.OpenOrder, 0, len(payload.EntrustedList))
	for _, order := range payload.EntrustedList {
		price := 0.0
		if strings.TrimSpace(order.Price) != "" {
			price, err = strconv.ParseFloat(order.Price, 64)
			if err != nil || price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
				return nil, fmt.Errorf("Bitget regular order %s has invalid price %q", order.OrderID, order.Price)
			}
		}
		quantity, err := strconv.ParseFloat(order.Size, 64)
		if err != nil || quantity <= 0 || math.IsNaN(quantity) || math.IsInf(quantity, 0) {
			return nil, fmt.Errorf("Bitget regular order %s has invalid size %q", order.OrderID, order.Size)
		}
		status := strings.ToUpper(order.State)
		if status == "" {
			status = "NEW"
		}
		result = append(result, types.OpenOrder{
			OrderID: order.OrderID, Symbol: symbol, Side: strings.ToUpper(order.Side),
			PositionSide: strings.ToUpper(order.PosSide), Type: strings.ToUpper(order.OrderType),
			Price: price, Quantity: quantity, Status: status,
		})
	}
	return result, nil
}

// GetProtectionSnapshot returns a side-specific, read-only TPSL snapshot with
// explicit absence, lookup failure, and duplicate-order ambiguity.
func (t *BitgetTrader) GetProtectionSnapshot(symbol, positionSide string) (types.ProtectionSnapshot, error) {
	result := types.ProtectionSnapshot{
		StopLoss:   types.ProtectionLevelSnapshot{Status: types.ProtectionConfirmedAbsent},
		TakeProfit: types.ProtectionLevelSnapshot{Status: types.ProtectionConfirmedAbsent},
	}
	normalizedSymbol := t.convertSymbol(symbol)
	records, err := t.getPendingPlanOrderRecords(normalizedSymbol)
	if err != nil {
		result.StopLoss.Status = types.ProtectionUnavailable
		result.TakeProfit.Status = types.ProtectionUnavailable
		return result, err
	}
	var snapshotErrors []error
	for _, record := range records {
		if !strings.EqualFold(record.Symbol, normalizedSymbol) {
			continue
		}
		orderType, supported := bitgetProtectionOrderType(record.PlanType)
		if !supported {
			result.StopLoss.Status = types.ProtectionAmbiguous
			result.TakeProfit.Status = types.ProtectionAmbiguous
			snapshotErrors = append(snapshotErrors, fmt.Errorf("Bitget TPSL order %s has unsupported planType %q", record.OrderID, record.PlanType))
			continue
		}
		level := &result.StopLoss
		label := "stop-loss"
		if orderType == "TAKE_PROFIT_MARKET" {
			level = &result.TakeProfit
			label = "take-profit"
		}
		order, _, parseErr := parseBitgetPendingPlanOrder(record)
		if parseErr != nil {
			if level.Status == types.ProtectionPresent || level.Status == types.ProtectionAmbiguous {
				level.Status = types.ProtectionAmbiguous
			} else {
				level.Status = types.ProtectionUnavailable
			}
			level.Price = 0
			level.OrderID = ""
			snapshotErrors = append(snapshotErrors, parseErr)
			continue
		}
		if positionSide != "" && order.PositionSide != "" && !strings.EqualFold(order.PositionSide, positionSide) {
			continue
		}
		if positionSide != "" && order.PositionSide == "" {
			level.Status = types.ProtectionAmbiguous
			level.Price = 0
			level.OrderID = ""
			snapshotErrors = append(snapshotErrors, fmt.Errorf("%s order %s has no position side", label, order.OrderID))
			continue
		}
		if level.Status != types.ProtectionConfirmedAbsent {
			level.Status = types.ProtectionAmbiguous
			level.Price = 0
			level.OrderID = ""
			snapshotErrors = append(snapshotErrors, fmt.Errorf("multiple active %s orders", label))
			continue
		}
		level.Status = types.ProtectionPresent
		level.Price = order.StopPrice
		level.OrderID = order.OrderID
	}
	if err := errors.Join(snapshotErrors...); err != nil {
		return result, fmt.Errorf("Bitget protection snapshot for %s %s: %w", symbol, positionSide, err)
	}
	return result, nil
}

// GetOpenOrders gets all open/pending orders for a symbol. Any source failure
// is returned instead of being silently collapsed into an empty list.
func (t *BitgetTrader) GetOpenOrders(symbol string) ([]types.OpenOrder, error) {
	symbol = t.convertSymbol(symbol)
	regularOrders, regularErr := t.getPendingRegularOrders(symbol)
	planOrders, planErr := t.getPendingPlanOrders(symbol)
	if err := errors.Join(regularErr, planErr); err != nil {
		return nil, err
	}
	result := append(regularOrders, planOrders...)
	logger.Infof("✓ BITGET GetOpenOrders: found %d open orders for %s", len(result), symbol)
	return result, nil
}

// PlaceLimitOrder places a limit order for grid trading
// Implements GridTrader interface
func (t *BitgetTrader) PlaceLimitOrder(req *types.LimitOrderRequest) (*types.LimitOrderResult, error) {
	symbol := t.convertSymbol(req.Symbol)

	// Set leverage if specified
	if req.Leverage > 0 {
		if err := t.SetLeverage(symbol, req.Leverage); err != nil {
			logger.Warnf("[Bitget] Failed to set leverage: %v", err)
		}
	}

	// Format quantity
	qtyStr, _ := t.FormatQuantity(symbol, req.Quantity)

	// Determine side
	side := "buy"
	if req.Side == "SELL" {
		side = "sell"
	}

	body := map[string]interface{}{
		"symbol":      symbol,
		"productType": "USDT-FUTURES",
		"marginMode":  "crossed",
		"marginCoin":  "USDT",
		"side":        side,
		"orderType":   "limit",
		"size":        qtyStr,
		"price":       fmt.Sprintf("%.8f", req.Price),
		"force":       "GTC", // Good Till Cancel
		"clientOid":   genBitgetClientOid(),
	}

	// Add reduce only if specified
	if req.ReduceOnly {
		body["reduceOnly"] = "YES"
	}

	logger.Infof("[Bitget] PlaceLimitOrder: %s %s @ %.4f, qty=%s", symbol, side, req.Price, qtyStr)

	data, err := t.doRequest("POST", bitgetOrderPath, body)
	if err != nil {
		return nil, fmt.Errorf("failed to place limit order: %w", err)
	}

	var order struct {
		OrderId   string `json:"orderId"`
		ClientOid string `json:"clientOid"`
	}

	if err := json.Unmarshal(data, &order); err != nil {
		return nil, fmt.Errorf("failed to parse order response: %w", err)
	}

	logger.Infof("✓ [Bitget] Limit order placed: %s %s @ %.4f, orderID=%s",
		symbol, side, req.Price, order.OrderId)

	return &types.LimitOrderResult{
		OrderID:      order.OrderId,
		ClientID:     order.ClientOid,
		Symbol:       req.Symbol,
		Side:         req.Side,
		PositionSide: req.PositionSide,
		Price:        req.Price,
		Quantity:     req.Quantity,
		Status:       "NEW",
	}, nil
}

// CancelOrder cancels a specific order by ID
// Implements GridTrader interface
func (t *BitgetTrader) CancelOrder(symbol, orderID string) error {
	symbol = t.convertSymbol(symbol)

	body := map[string]interface{}{
		"symbol":      symbol,
		"productType": "USDT-FUTURES",
		"orderId":     orderID,
	}

	_, err := t.doRequest("POST", "/api/v2/mix/order/cancel-order", body)
	if err != nil {
		return fmt.Errorf("failed to cancel order: %w", err)
	}

	logger.Infof("✓ [Bitget] Order cancelled: %s %s", symbol, orderID)
	return nil
}
