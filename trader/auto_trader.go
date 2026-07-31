package trader

import (
	"fmt"
	"github.com/ethereum/go-ethereum/crypto"
	"nofx/kernel"
	"nofx/logger"
	"nofx/market"
	"nofx/mcp"
	_ "nofx/mcp/payment"
	_ "nofx/mcp/provider"
	"nofx/store"
	"nofx/trader/aster"
	"nofx/trader/binance"
	"nofx/trader/bitget"
	"nofx/trader/bybit"
	"nofx/trader/gate"
	"nofx/trader/hyperliquid"
	"nofx/trader/indodax"
	"nofx/trader/kucoin"
	"nofx/trader/lighter"
	"nofx/trader/okx"
	"nofx/wallet"
	"strings"
	"sync"
	"time"
)

// Keep paper risk refreshes independent from the AI loop while avoiding a
// high-frequency Binance poll for every paper trader. The broker skips the
// refresh entirely while it has no positions or pending orders.
const defaultPaperRiskMonitorInterval = 30 * time.Second

func (at *AutoTrader) logTag() string {
	if at == nil {
		return "[trader_id=unknown]"
	}
	if at.name != "" {
		return fmt.Sprintf("[trader_id=%s trader_name=%s]", at.id, at.name)
	}
	return fmt.Sprintf("[trader_id=%s]", at.id)
}

func (at *AutoTrader) logInfof(format string, args ...interface{}) {
	values := append([]interface{}{at.logTag()}, args...)
	logger.Infof("%s "+format, values...)
}

func (at *AutoTrader) logWarnf(format string, args ...interface{}) {
	values := append([]interface{}{at.logTag()}, args...)
	logger.Warnf("%s "+format, values...)
}

func (at *AutoTrader) logErrorf(format string, args ...interface{}) {
	values := append([]interface{}{at.logTag()}, args...)
	logger.Errorf("%s "+format, values...)
}

// AutoTraderConfig auto trading configuration (simplified version - AI makes all decisions)
type AutoTraderConfig struct {
	// Trader identification
	ID         string // Trader unique identifier (for log directory, etc.)
	Name       string // Trader display name
	StrategyID string // Associated strategy ID used to refresh live strategy config
	AIModel    string // AI model: "qwen" or "deepseek"

	// Trading platform selection
	Exchange      string // Exchange type: "binance", "bybit", "okx", "bitget", "gate", "hyperliquid", "aster" or "lighter"
	ExchangeID    string // Exchange account UUID (for multi-account support)
	ExecutionMode ExecutionMode

	// Binance API configuration
	BinanceAPIKey    string
	BinanceSecretKey string

	// Bybit API configuration
	BybitAPIKey    string
	BybitSecretKey string

	// OKX API configuration
	OKXAPIKey     string
	OKXSecretKey  string
	OKXPassphrase string

	// Bitget API configuration
	BitgetAPIKey     string
	BitgetSecretKey  string
	BitgetPassphrase string

	// Gate API configuration
	GateAPIKey    string
	GateSecretKey string

	// KuCoin API configuration
	KuCoinAPIKey     string
	KuCoinSecretKey  string
	KuCoinPassphrase string

	// Indodax API configuration
	IndodaxAPIKey    string
	IndodaxSecretKey string

	// Hyperliquid configuration
	HyperliquidPrivateKey  string
	HyperliquidWalletAddr  string
	HyperliquidTestnet     bool
	HyperliquidUnifiedAcct bool // Unified Account mode: Spot USDC as Perp collateral

	// Aster configuration
	AsterUser       string // Aster main wallet address
	AsterSigner     string // Aster API wallet address
	AsterPrivateKey string // Aster API wallet private key

	// LIGHTER configuration
	LighterWalletAddr       string // LIGHTER wallet address (L1 wallet)
	LighterPrivateKey       string // LIGHTER L1 private key (for account identification)
	LighterAPIKeyPrivateKey string // LIGHTER API Key private key (40 bytes, for transaction signing)
	LighterAPIKeyIndex      int    // LIGHTER API Key index (0-255)
	LighterTestnet          bool   // Whether to use testnet

	// AI configuration
	UseQwen     bool
	DeepSeekKey string
	QwenKey     string

	// Custom AI API configuration
	CustomAPIURL      string
	CustomAPIKey      string
	CustomModelName   string
	Claw402WalletKey  string
	AIModelCandidates []AIModelCandidate

	// Scan configuration
	ScanInterval                time.Duration // Scan interval (recommended 15 minutes)
	StartupDelay                time.Duration // Delay before the first cycle after startup
	PaperRiskMonitorInterval    time.Duration // Paper-only mark/SL/TP/liquidation refresh interval
	PaperFundingMonitorInterval time.Duration // Paper-only funding snapshot/settlement cadence

	// Account configuration
	InitialBalance float64 // Initial balance (for P&L calculation, must be set manually)

	// Risk control (only as hints, AI can make autonomous decisions)
	MaxDailyLoss    float64       // Maximum daily loss percentage (hint)
	MaxDrawdown     float64       // Maximum drawdown percentage (hint)
	StopTradingTime time.Duration // Pause duration after risk control triggers

	// Position mode
	IsCrossMargin bool // true=cross margin mode, false=isolated margin mode

	// Signal inversion
	InvertSignals bool // true=invert AI trading decisions (open_long <-> open_short, close_long <-> close_short)

	// Competition visibility
	ShowInCompetition bool // Whether to show in competition page

	// Strategy configuration (use complete strategy config)
	StrategyConfig    *store.StrategyConfig // Strategy configuration (includes coin sources, indicators, risk control, prompts, etc.)
	StrategyConfigRaw string                // Raw strategy config JSON from DB, used to detect live edits
}

// AIModelCandidate describes one model in the trader's ordered failover chain.
// Candidates with the same provider and endpoint may represent model variants
// such as Sol, Terra, and Luna; independent provider candidates are appended
// after those variants.
type AIModelCandidate struct {
	ID           string
	Provider     string
	APIKey       string
	CustomAPIURL string
	ModelName    string
}

type binanceMarketAvailabilityClient interface {
	ValidateMarketAvailability(symbol string) (*market.MarketAvailability, error)
}

// AutoTrader automatic trader
type AutoTrader struct {
	id                    string // Trader unique identifier
	name                  string // Trader display name
	aiModel               string // AI model name
	exchange              string // Trading platform type (binance/bybit/etc)
	executionMode         ExecutionMode
	paperBroker           *PaperBroker
	binanceMarketClient   binanceMarketAvailabilityClient // Shared-cache public client used for Binance open preflight
	exchangeID            string                          // Exchange account UUID
	showInCompetition     bool                            // Whether to show in competition page
	invertSignals         bool                            // Whether to invert AI trading decisions
	config                AutoTraderConfig
	trader                Trader // Use Trader interface (supports multiple platforms)
	mcpClient             mcp.AIClient
	cycleRunner           func() error           // Optional injected cycle runner for deterministic lifecycle tests
	store                 *store.Store           // Data storage (decision records, etc.)
	strategyEngine        *kernel.StrategyEngine // Strategy engine (uses strategy configuration)
	cycleNumber           int                    // Current cycle number
	initialBalance        float64
	dailyPnL              float64
	customPrompt          string // Custom trading strategy prompt
	overrideBasePrompt    bool   // Whether to override base prompt
	lastResetTime         time.Time
	stopUntil             time.Time
	isRunning             bool
	isRunningMutex        sync.RWMutex       // Mutex to protect isRunning flag
	startTime             time.Time          // System start time
	callCount             int                // AI call count
	positionFirstSeenTime map[string]int64   // Position first seen time (symbol_side -> timestamp in milliseconds)
	stopMonitorCh         chan struct{}      // Used to stop monitoring goroutine
	monitorWg             sync.WaitGroup     // Used to wait for monitoring goroutine to finish
	peakPnLCache          map[string]float64 // Peak profit cache (symbol -> peak P&L percentage)
	peakPnLCacheMutex     sync.RWMutex       // Cache read-write lock
	lastBalanceSyncTime   time.Time          // Last balance sync time
	userID                string             // User ID
	gridState             *GridState         // Grid trading state (only used when StrategyType == "grid_trading")
	claw402WalletAddr     string             // Claw402 wallet address (derived from private key at start)
	runStopCh             chan struct{}      // Stops only the AI decision loop when the trader is paused
	consecutiveAIFailures int                // Consecutive AI call failures
	monitorLifecycleMu    sync.Mutex         // Guards background monitor startup and shutdown
	monitorsStarted       bool               // Background position/risk monitors are running
	monitorsStopped       bool               // This trader instance has been permanently shut down
	runtimeHealthMu       sync.RWMutex       // Guards safe mode + AI wallet health (loop writes, API reads)
	safeMode              bool               // Safe mode: no new positions, protect existing ones
	safeModeReason        string             // Why safe mode was activated
	aiWalletStatus        string             // "ok"|"low"|"empty"|"unknown" — see runtime_health.go
	aiWalletBalanceUSDC   float64            // Last observed Base USDC balance of the claw402 wallet
	aiWalletCheckedAt     time.Time          // When the balance was last observed
}

// NewAutoTrader creates an automatic trader
// st parameter is used to store decision records to database
func NewAutoTrader(config AutoTraderConfig, st *store.Store, userID string) (*AutoTrader, error) {
	// Set default values
	if config.PaperRiskMonitorInterval <= 0 {
		config.PaperRiskMonitorInterval = defaultPaperRiskMonitorInterval
	}
	if config.PaperFundingMonitorInterval <= 0 {
		config.PaperFundingMonitorInterval = time.Minute
	}
	if config.ID == "" {
		config.ID = "default_trader"
	}
	if config.Name == "" {
		config.Name = "Default Trader"
	}
	if config.AIModel == "" {
		if config.UseQwen {
			config.AIModel = "qwen"
		} else {
			config.AIModel = "deepseek"
		}
	}

	// Initialize the primary client and optional model failover chain.
	var mcpClient mcp.AIClient
	aiModel := config.AIModel
	if len(config.AIModelCandidates) > 0 {
		failoverCandidates := make([]failoverCandidate, 0, len(config.AIModelCandidates))
		for _, candidateConfiguration := range config.AIModelCandidates {
			candidateClient, clientErr := newAIClientForCandidate(candidateConfiguration)
			if clientErr != nil {
				return nil, clientErr
			}
			failoverCandidates = append(failoverCandidates, failoverCandidate{
				configuration: candidateConfiguration,
				client:        candidateClient,
			})
		}
		failoverClient, failoverErr := newAIModelFailoverClient(failoverCandidates)
		if failoverErr != nil {
			return nil, failoverErr
		}
		mcpClient = failoverClient
		aiModel = config.AIModelCandidates[0].Provider
		logger.Infof("🤖 [%s] Using model failover chain starting with %s/%s (%d candidates)",
			config.Name,
			config.AIModelCandidates[0].Provider,
			modelDisplayName(config.AIModelCandidates[0]),
			len(config.AIModelCandidates))
	} else {
		if config.UseQwen && aiModel == "" {
			aiModel = "qwen"
		}

		// Resolve API key (provider-specific overrides).
		apiKey := config.CustomAPIKey
		customURL := config.CustomAPIURL
		switch aiModel {
		case "qwen":
			if config.QwenKey != "" {
				apiKey = config.QwenKey
			}
		case "deepseek", "":
			if config.DeepSeekKey != "" {
				apiKey = config.DeepSeekKey
			}
		}

		if aiModel == "custom" {
			mcpClient = mcp.New()
		} else if aiModel == "" {
			aiModel = "deepseek"
			mcpClient = mcp.NewAIClientByProvider(aiModel)
		} else {
			mcpClient = mcp.NewAIClientByProvider(aiModel)
		}
		if mcpClient == nil {
			return nil, fmt.Errorf("unsupported AI provider %q; configure a registered provider or custom endpoint", aiModel)
		}

		// Payment providers (claw402) ignore customURL.
		switch aiModel {
		case "claw402":
			mcpClient.SetAPIKey(apiKey, "", config.CustomModelName)
		default:
			mcpClient.SetAPIKey(apiKey, customURL, config.CustomModelName)
			if customURL != "" {
				if configurator, ok := mcpClient.(mcp.CustomURLConfigurator); ok {
					if err := configurator.ConfigureCustomURL(customURL); err != nil {
						return nil, fmt.Errorf("invalid custom model URL: %w", err)
					}
				}
			}
		}
		logger.Infof("🤖 [%s] Using %s AI", config.Name, aiModel)

		if config.CustomAPIURL != "" || config.CustomModelName != "" {
			logger.Infof("🔧 [%s] Custom config - URL: %s, Model: %s", config.Name, config.CustomAPIURL, config.CustomModelName)
		}
	}

	// Set default trading platform
	if config.Exchange == "" {
		config.Exchange = "binance"
	}
	if config.ExecutionMode == "" {
		config.ExecutionMode = ExecutionModePaper
	}
	if config.ExecutionMode != ExecutionModePaper && config.ExecutionMode != ExecutionModeLive {
		return nil, fmt.Errorf("unsupported execution mode: %s", config.ExecutionMode)
	}

	// Create corresponding trader based on configuration
	var trader Trader
	var paperBroker *PaperBroker
	var err error
	var binanceMarketClient *market.APIClient
	if config.ExecutionMode == ExecutionModePaper || strings.EqualFold(config.Exchange, "binance") {
		binanceMarketClient = market.NewAPIClient()
	}

	// Record position mode (general)
	marginModeStr := "Cross Margin"
	if !config.IsCrossMargin {
		marginModeStr = "Isolated Margin"
	}
	logger.Infof("📊 [%s] Position mode: %s", config.Name, marginModeStr)

	if config.ExecutionMode == ExecutionModePaper {
		if config.InitialBalance <= 0 {
			config.InitialBalance = 10_000
		}
		priceSource := &binancePaperPriceSource{client: binanceMarketClient}
		paperConfig := PaperBrokerConfig{
			InitialBalance: config.InitialBalance,
			MakerFirst:     true, MakerFeeBPS: 2, TakerFeeBPS: 5, SlippageBPS: 2,
			MakerTimeout: 15 * time.Second, MakerMaxReprices: 2,
			FundingSource: priceSource,
		}
		if st != nil {
			paperBroker, err = NewPersistentPaperBroker(paperConfig, priceSource, st.Paper(), config.ID)
		} else {
			paperBroker, err = NewPaperBroker(paperConfig, priceSource)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to initialize paper broker: %w", err)
		}
		trader = paperBroker
		logger.Infof("🧪 [%s] Paper Trading enabled; no exchange write client was created", config.Name)
	} else {
		switch config.Exchange {
		case "binance":
			logger.Infof("🏦 [%s] Using Binance Futures trading", config.Name)
			trader = binance.NewFuturesTrader(config.BinanceAPIKey, config.BinanceSecretKey, userID)
		case "bybit":
			logger.Infof("🏦 [%s] Using Bybit Futures trading", config.Name)
			trader = bybit.NewBybitTrader(config.BybitAPIKey, config.BybitSecretKey)
		case "okx":
			logger.Infof("🏦 [%s] Using OKX Futures trading", config.Name)
			trader = okx.NewOKXTrader(config.OKXAPIKey, config.OKXSecretKey, config.OKXPassphrase)
		case "bitget":
			logger.Infof("🏦 [%s] Using Bitget Futures trading", config.Name)
			trader = bitget.NewBitgetTrader(config.BitgetAPIKey, config.BitgetSecretKey, config.BitgetPassphrase)
		case "gate":
			logger.Infof("🏦 [%s] Using Gate.io Futures trading", config.Name)
			trader = gate.NewGateTrader(config.GateAPIKey, config.GateSecretKey)
		case "kucoin":
			logger.Infof("🏦 [%s] Using KuCoin Futures trading", config.Name)
			trader = kucoin.NewKuCoinTrader(config.KuCoinAPIKey, config.KuCoinSecretKey, config.KuCoinPassphrase)
		case "hyperliquid":
			logger.Infof("🏦 [%s] Using Hyperliquid trading", config.Name)
			trader, err = hyperliquid.NewHyperliquidTrader(config.HyperliquidPrivateKey, config.HyperliquidWalletAddr, config.HyperliquidTestnet, config.HyperliquidUnifiedAcct)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize Hyperliquid trader: %w", err)
			}
		case "aster":
			logger.Infof("🏦 [%s] Using Aster trading", config.Name)
			trader, err = aster.NewAsterTrader(config.AsterUser, config.AsterSigner, config.AsterPrivateKey)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize Aster trader: %w", err)
			}
		case "lighter":
			logger.Infof("🏦 [%s] Using LIGHTER trading", config.Name)

			if config.LighterWalletAddr == "" || config.LighterAPIKeyPrivateKey == "" {
				return nil, fmt.Errorf("Lighter requires wallet address and API Key private key")
			}

			// Lighter only supports mainnet (testnet disabled)
			trader, err = lighter.NewLighterTraderV2(
				config.LighterWalletAddr,
				config.LighterAPIKeyPrivateKey,
				config.LighterAPIKeyIndex,
				false, // Always use mainnet for Lighter
			)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize LIGHTER trader: %w", err)
			}
			logger.Infof("✓ LIGHTER trader initialized successfully")
		case "indodax":
			logger.Infof("🏦 [%s] Using Indodax Spot trading", config.Name)
			trader = indodax.NewIndodaxTrader(config.IndodaxAPIKey, config.IndodaxSecretKey)
		default:
			return nil, fmt.Errorf("unsupported trading platform: %s", config.Exchange)
		}
	}

	// Validate initial balance configuration, auto-fetch from exchange if 0
	if config.InitialBalance <= 0 {
		logger.Infof("📊 [%s] Initial balance not set, attempting to fetch current balance from exchange...", config.Name)
		account, err := trader.GetBalance()
		if err != nil {
			return nil, fmt.Errorf("initial balance not set and unable to fetch balance from exchange: %w", err)
		}
		// Try multiple balance field names (different exchanges return different formats)
		balanceKeys := []string{"total_equity", "totalWalletBalance", "wallet_balance", "totalEq", "balance"}
		var foundBalance float64
		for _, key := range balanceKeys {
			if balance, ok := account[key].(float64); ok && balance > 0 {
				foundBalance = balance
				break
			}
		}
		if foundBalance > 0 {
			config.InitialBalance = foundBalance
			logger.Infof("✓ [%s] Auto-fetched initial balance: %.2f USDT", config.Name, foundBalance)
			// Save to database so it persists across restarts
			if st != nil {
				if err := st.Trader().UpdateInitialBalance(userID, config.ID, foundBalance); err != nil {
					logger.Infof("⚠️  [%s] Failed to save initial balance to database: %v", config.Name, err)
				} else {
					logger.Infof("✓ [%s] Initial balance saved to database", config.Name)
				}
			}
		} else {
			return nil, fmt.Errorf("initial balance must be greater than 0, please set InitialBalance in config or ensure exchange account has balance")
		}
	}

	// Get last cycle number (for recovery)
	var cycleNumber int
	if st != nil {
		cycleNumber, _ = st.Decision().GetLastCycleNumber(config.ID)
		logger.Infof("📊 [%s] Decision records will be stored to database", config.Name)
	}

	// Create strategy engine (must have strategy config)
	if config.StrategyConfig == nil {
		return nil, fmt.Errorf("[%s] strategy not configured", config.Name)
	}
	// Pass claw402 wallet key to strategy engine so nofxos data requests
	// are routed through claw402 (reuses the same wallet as AI calls)
	claw402Key := config.Claw402WalletKey
	if claw402Key == "" && config.AIModel == "claw402" && config.CustomAPIKey != "" {
		claw402Key = config.CustomAPIKey
	}
	strategyEngine := kernel.NewStrategyEngine(config.StrategyConfig, claw402Key)
	logger.Infof("✓ [%s] Using strategy engine (strategy configuration loaded)", config.Name)

	return &AutoTrader{
		id:                    config.ID,
		name:                  config.Name,
		aiModel:               config.AIModel,
		exchange:              config.Exchange,
		executionMode:         config.ExecutionMode,
		paperBroker:           paperBroker,
		binanceMarketClient:   binanceMarketClient,
		exchangeID:            config.ExchangeID,
		showInCompetition:     config.ShowInCompetition,
		invertSignals:         config.InvertSignals,
		config:                config,
		trader:                trader,
		mcpClient:             mcpClient,
		store:                 st,
		strategyEngine:        strategyEngine,
		cycleNumber:           cycleNumber,
		initialBalance:        config.InitialBalance,
		lastResetTime:         time.Now(),
		startTime:             time.Now(),
		callCount:             0,
		isRunning:             false,
		positionFirstSeenTime: make(map[string]int64),
		stopMonitorCh:         make(chan struct{}),
		runStopCh:             make(chan struct{}),
		monitorWg:             sync.WaitGroup{},
		peakPnLCache:          make(map[string]float64),
		peakPnLCacheMutex:     sync.RWMutex{},
		lastBalanceSyncTime:   time.Now(),
		userID:                userID,
	}, nil
}

func (at *AutoTrader) reloadStrategyConfigIfChanged() error {
	if at == nil || at.store == nil || at.config.StrategyID == "" {
		return nil
	}

	strategy, err := at.store.Strategy().Get(at.userID, at.config.StrategyID)
	if err != nil {
		return fmt.Errorf("failed to load strategy %s: %w", at.config.StrategyID, err)
	}

	if at.strategyEngine != nil && strategy.Config == at.config.StrategyConfigRaw {
		return nil
	}

	strategyConfig, err := strategy.ParseConfig()
	if err != nil {
		return fmt.Errorf("failed to parse strategy %s: %w", strategy.Name, err)
	}
	strategyConfig.ClampLimits()

	// NOTE: this used to hardcode the Autopilot book shape (6 positions ×
	// equity×1.2 notional), silently overriding whatever the user configured in
	// their strategy. Sizing now comes from the strategy's own RiskControl —
	// ClampLimits above bounds it (ratio 0.5–10, leverage caps), and the
	// margin auto-reduce at order time keeps the book solvent.

	claw402Key := at.config.Claw402WalletKey
	if claw402Key == "" && at.config.AIModel == "claw402" && at.config.CustomAPIKey != "" {
		claw402Key = at.config.CustomAPIKey
	}

	at.config.StrategyConfig = strategyConfig
	at.config.StrategyConfigRaw = strategy.Config
	at.strategyEngine = kernel.NewStrategyEngine(strategyConfig, claw402Key)
	at.logInfof("🔄 Strategy config refreshed from DB: %s", strategy.Name)
	return nil
}

// GetStartupDelay returns the configured delay before the first trading cycle.
func (at *AutoTrader) GetStartupDelay() time.Duration {
	return at.config.StartupDelay
}

// RunWithStartupDelay starts the runtime with a manager-supplied delay. The
// manager uses this for deterministic 0/5/10 minute startup staggering when a
// trader has no explicit delay configured.
func (at *AutoTrader) RunWithStartupDelay(startupDelay time.Duration) error {
	if startupDelay > 0 {
		at.config.StartupDelay = startupDelay
	}
	return at.Run()
}

// Run runs the automatic trading main loop
func (at *AutoTrader) Run() error {
	at.isRunningMutex.Lock()
	if at.isRunning {
		at.isRunningMutex.Unlock()
		at.logWarnf("⚠️ Trader runtime is already running; duplicate Run ignored")
		return nil
	}
	at.isRunning = true
	at.runStopCh = make(chan struct{})
	runStopCh := at.runStopCh
	at.startTime = time.Now()
	at.isRunningMutex.Unlock()

	logger.Info("🚀 AI-driven automatic trading system started")
	at.logInfof("💰 Initial balance: %.2f USDT", at.initialBalance)
	at.logInfof("⚙️  Scan interval: %v", at.config.ScanInterval)
	logger.Info("🤖 AI will make full decisions on leverage, position size, stop loss/take profit, etc.")

	if at.config.StartupDelay > 0 {
		at.logInfof("⏳ Startup stagger enabled; first trading cycle will begin after %v", at.config.StartupDelay)
		startupTimer := time.NewTimer(at.config.StartupDelay)
		select {
		case <-startupTimer.C:
		case <-runStopCh:
			if !startupTimer.Stop() {
				select {
				case <-startupTimer.C:
				default:
				}
			}
			return nil
		}
	}

	// Pre-launch checks for claw402 users
	at.runPreLaunchChecks()

	at.StartBackgroundMonitoring()

	// Check if this is a grid trading strategy
	isGridStrategy := at.IsGridStrategy()
	if isGridStrategy {
		at.logInfof("🔲 Grid trading strategy detected, initializing grid...")
		if err := at.InitializeGrid(); err != nil {
			at.logErrorf("❌ Failed to initialize grid: %v", err)
			return fmt.Errorf("grid initialization failed: %w", err)
		}
	}

	// Execute immediately on first run
	at.logInfof("▶️ Running first trading cycle immediately; next cycle starts after %v", at.config.ScanInterval)
	if isGridStrategy {
		if err := at.RunGridCycle(); err != nil {
			at.logErrorf("❌ Grid execution failed: %v", err)
		}
	} else {
		if err := at.runAutomaticCycle(); err != nil {
			at.logErrorf("❌ Execution failed: %v", err)
		}
	}

	ticker := time.NewTicker(at.config.ScanInterval)
	defer ticker.Stop()

	for {
		at.isRunningMutex.RLock()
		running := at.isRunning
		at.isRunningMutex.RUnlock()

		if !running {
			break
		}

		select {
		case <-ticker.C:
			if isGridStrategy {
				if err := at.RunGridCycle(); err != nil {
					at.logErrorf("❌ Grid execution failed: %v", err)
				}
			} else {
				if err := at.runAutomaticCycle(); err != nil {
					at.logErrorf("❌ Execution failed: %v", err)
				}
			}
		case <-runStopCh:
			at.logInfof("⏸ Pause signal received, exiting automatic trading decision loop")
			return nil
		}
	}

	return nil
}

func (at *AutoTrader) runAutomaticCycle() error {
	if at.cycleRunner != nil {
		return at.cycleRunner()
	}
	return at.runCycle()
}

// Stop pauses automatic AI decisions while leaving position/risk monitoring active.
func (at *AutoTrader) Stop() {
	at.isRunningMutex.Lock()
	if !at.isRunning {
		at.isRunningMutex.Unlock()
		return
	}
	at.isRunning = false
	runStopCh := at.runStopCh
	at.isRunningMutex.Unlock()

	if runStopCh != nil {
		close(runStopCh)
	}
	logger.Info("⏸ Automatic AI trading paused; position monitoring remains active")
}

// GetID gets trader ID
func (at *AutoTrader) GetID() string {
	return at.id
}

// GetInitialBalance returns the account baseline used for performance metrics
// (e.g. the drawdown equity curve).
func (at *AutoTrader) GetInitialBalance() float64 {
	return at.initialBalance
}

// GetUnderlyingTrader returns the underlying Trader interface implementation
// This is used by grid trading and other components that need direct exchange access
func (at *AutoTrader) GetUnderlyingTrader() Trader {
	return at.trader
}

// GetName gets trader name
func (at *AutoTrader) GetName() string {
	return at.name
}

// GetAIModel gets AI model
func (at *AutoTrader) GetAIModel() string {
	return at.aiModel
}

// GetExchange gets exchange
func (at *AutoTrader) GetExchange() string {
	return at.exchange
}

// GetShowInCompetition returns whether trader should be shown in competition
func (at *AutoTrader) GetShowInCompetition() bool {
	return at.showInCompetition
}

// GetInvertSignals returns whether AI decisions should be inverted
func (at *AutoTrader) GetInvertSignals() bool {
	return at.invertSignals
}

// SetShowInCompetition sets whether trader should be shown in competition
func (at *AutoTrader) SetShowInCompetition(show bool) {
	at.showInCompetition = show
}

// SetCustomPrompt sets custom trading strategy prompt
func (at *AutoTrader) SetCustomPrompt(prompt string) {
	at.customPrompt = prompt
}

// SetOverrideBasePrompt sets whether to override base prompt
func (at *AutoTrader) SetOverrideBasePrompt(override bool) {
	at.overrideBasePrompt = override
}

// GetSystemPromptTemplate gets current system prompt template name (from strategy config)
func (at *AutoTrader) GetSystemPromptTemplate() string {
	if at.strategyEngine != nil {
		config := at.strategyEngine.GetConfig()
		if config.CustomPrompt != "" {
			return "custom"
		}
	}
	return "strategy"
}

// GetCandidateCoins returns the current candidate coin set from the trader's strategy engine.
func (at *AutoTrader) GetCandidateCoins() ([]kernel.CandidateCoin, error) {
	if at.strategyEngine == nil {
		return nil, fmt.Errorf("strategy engine not configured")
	}
	return at.strategyEngine.GetCandidateCoins()
}

// GetStrategyConfig returns the current strategy config used by the trader.
func (at *AutoTrader) GetStrategyConfig() *store.StrategyConfig {
	if at.strategyEngine == nil {
		return at.config.StrategyConfig
	}
	return at.strategyEngine.GetConfig()
}

// GetStore gets data store (for external access to decision records, etc.)
func (at *AutoTrader) GetStore() *store.Store {
	return at.store
}

// calculatePnLPercentage calculates P&L percentage (based on margin, automatically considers leverage)
// Return rate = Unrealized P&L / Margin x 100%
func calculatePnLPercentage(unrealizedPnl, marginUsed float64) float64 {
	if marginUsed > 0 {
		return (unrealizedPnl / marginUsed) * 100
	}
	return 0.0
}

// runPreLaunchChecks performs pre-launch checks for claw402 users (wallet balance, runway estimate)
func (at *AutoTrader) runPreLaunchChecks() {
	if !store.IsClaw402Config(at.config.AIModel) {
		return
	}

	logger.Info("🔍 Running pre-launch checks (claw402)...")

	// Derive wallet address from CustomAPIKey (which is the private key for claw402)
	if at.config.CustomAPIKey != "" {
		// Try to derive address using go-ethereum
		addr := deriveWalletAddress(at.config.CustomAPIKey)
		if addr != "" {
			at.claw402WalletAddr = addr
			logger.Infof("💳 [%s] Claw402 wallet: %s", at.name, addr)

			// Query USDC balance
			balance, err := wallet.QueryUSDCBalance(addr)
			if err != nil {
				logger.Warnf("⚠️ [%s] Could not query USDC balance: %v", at.name, err)
				at.markAIWalletHealthUnknown()
			} else {
				at.setAIWalletHealth(balance)
				// Estimate runway
				scanMinutes := int(at.config.ScanInterval.Minutes())
				modelName := at.config.CustomModelName
				if modelName == "" {
					modelName = "deepseek"
				}
				dailyCost, runway := store.EstimateRunway(balance, modelName, scanMinutes)
				logger.Infof("💰 [%s] USDC Balance: $%.2f | Daily AI cost: ~$%.2f | Runway: ~%.1f days",
					at.name, balance, dailyCost, runway)

				if balance < 1.0 {
					logger.Warnf("⚠️ [%s] Low USDC balance! Consider topping up.", at.name)
				}
				if balance <= 0 {
					logger.Errorf("🚨 [%s] USDC balance is ZERO — AI calls will fail!", at.name)
				}
			}
		}
	}

	logger.Info("✅ Pre-launch checks complete")
}

// deriveWalletAddress derives an Ethereum address from a hex private key
func deriveWalletAddress(privateKeyHex string) string {
	// Remove 0x prefix if present
	if len(privateKeyHex) > 2 && privateKeyHex[:2] == "0x" {
		privateKeyHex = privateKeyHex[2:]
	}

	privateKey, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return ""
	}

	address := crypto.PubkeyToAddress(privateKey.PublicKey)
	return address.Hex()
}
