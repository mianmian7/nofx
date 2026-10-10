package signals

import (
	"context"
	"encoding/json"
	"fmt"
	"nofx/logger"
	"nofx/market"
	"nofx/store"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var Exchanges = []string{"binance", "okx", "bitget"}

const ScanLimit = 20
const ScanInterval = 5 * time.Minute

type Repository interface {
	Latest(string) (*store.LocalSignalScan, error)
	Save(*store.LocalSignalScan) error
	History(string, string, int) ([]store.LocalSignal, error)
	HistoryBefore(string, string, int64, int) ([]store.LocalSignal, error)
	Prune(time.Time) error
}
type Snapshot struct {
	store.LocalSignalScan
	Ready      bool   `json:"ready"`
	Scanning   bool   `json:"scanning"`
	Stale      bool   `json:"stale"`
	NextScanAt int64  `json:"next_scan_at"`
	Version    string `json:"version"`
}

type Service struct {
	repo     Repository
	now      func() time.Time
	provider func(string) (market.MarketDataProvider, error)
	mu       sync.RWMutex
	latest   map[string]*store.LocalSignalScan
	ready    map[string]bool
	running  map[string]bool
	next     map[string]int64
	slots    chan struct{}
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

var defaultService atomic.Pointer[Service]

func SetDefault(s *Service) { defaultService.Store(s) }
func Default() *Service     { return defaultService.Load() }

func Supported(exchange string) bool {
	for _, v := range Exchanges {
		if exchange == v {
			return true
		}
	}
	return false
}

func New(repo Repository) *Service {
	s := &Service{repo: repo, now: time.Now, provider: market.NewMarketDataProvider, latest: make(map[string]*store.LocalSignalScan), ready: make(map[string]bool), running: make(map[string]bool), next: make(map[string]int64), slots: make(chan struct{}, 2)}
	for _, ex := range Exchanges {
		if row, err := repo.Latest(ex); err == nil {
			s.latest[ex] = row
		}
	}
	return s
}

func (s *Service) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	for i, ex := range Exchanges {
		s.wg.Add(1)
		go func(exchange string, delay time.Duration) {
			defer s.wg.Done()
			deadline := time.Now().Add(delay)
			timer := time.NewTimer(time.Until(deadline))
			defer timer.Stop()
			catchingUp := false
			for {
				s.mu.Lock()
				s.next[exchange] = deadline.UnixMilli()
				s.mu.Unlock()
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
				}
				started := time.Now()
				if err := s.Scan(ctx, exchange); err != nil && ctx.Err() == nil {
					logger.Warnf("Local market analysis %s: %v", exchange, err)
				}
				deadline, catchingUp = nextScanDeadline(exchange, started, time.Now(), catchingUp)
				timer.Reset(time.Until(deadline))
			}
		}(ex, time.Duration(i)*15*time.Second)
	}
}

// Refresh on fixed wall-clock boundaries instead of accumulating request time.
// The short venue offsets allow a newly closed candle to reach the exchange API
// and spread the work over the existing shared concurrency limit.
func nextAlignedScan(exchange string, now time.Time) time.Time {
	offset := 5 * time.Second
	for i, venue := range Exchanges {
		if venue == exchange {
			offset += time.Duration(i) * 15 * time.Second
			break
		}
	}
	next := now.Truncate(ScanInterval).Add(offset)
	if !next.After(now) {
		next = next.Add(ScanInterval)
	}
	return next
}

func nextScanDeadline(exchange string, started, finished time.Time, catchingUp bool) (time.Time, bool) {
	// A startup/slow scan may straddle the next boundary. Fetch the latest
	// candles once more, but never replay a backlog or chain catch-up scans.
	if !catchingUp && !nextAlignedScan(exchange, started).After(finished) {
		return finished.Add(time.Second), true
	}
	return nextAlignedScan(exchange, finished), false
}

func (s *Service) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

func (s *Service) Snapshot(exchange string) (Snapshot, error) {
	if s == nil {
		return Snapshot{}, fmt.Errorf("local market analysis is not running")
	}
	if !Supported(exchange) {
		return Snapshot{}, fmt.Errorf("local analysis does not support %q", exchange)
	}
	s.mu.RLock()
	row := s.latest[exchange]
	out := Snapshot{Ready: s.ready[exchange], Scanning: s.running[exchange], NextScanAt: s.next[exchange], Version: Version}
	if row != nil {
		// Returned slices must not permit API/kernel callers to mutate the
		// immutable observation used concurrently at the execution boundary.
		encoded, _ := json.Marshal(row)
		_ = json.Unmarshal(encoded, &out.LocalSignalScan)
	} else {
		out.Exchange = exchange
		out.Results = []store.LocalSignal{}
		out.Failures = []string{}
	}
	s.mu.RUnlock()
	now := s.now()
	fresh := 0
	for _, result := range out.Results {
		if result.Exchange == exchange && Fresh(result, now) {
			fresh++
		}
	}
	out.Stale = out.ObservedAt <= 0 || now.Sub(time.UnixMilli(out.ObservedAt)) > 10*time.Minute || (out.Succeeded > 0 && fresh*5 < out.Expected*4)
	out.Ready = out.Ready && !out.Stale && out.Error == "" && out.Expected > 0 && fresh*5 >= out.Expected*4
	return out, nil
}

func (s *Service) History(exchange, symbol string, limit int) ([]store.LocalSignal, error) {
	if s == nil {
		return nil, fmt.Errorf("local market analysis is not running")
	}
	return s.repo.History(exchange, symbol, limit)
}

func (s *Service) HistoryBefore(exchange, symbol string, before int64, limit int) ([]store.LocalSignal, error) {
	if s == nil {
		return nil, fmt.Errorf("local market analysis is not running")
	}
	return s.repo.HistoryBefore(exchange, symbol, before, limit)
}

// Candidates never substitutes another venue or the old volume-only pool.
func (s *Service) Candidates(exchange string, minScore float64, limit int) ([]store.LocalSignal, error) {
	snapshot, err := s.Snapshot(exchange)
	if err != nil {
		return nil, err
	}
	if !snapshot.Ready {
		reason := snapshot.Error
		if reason == "" {
			reason = "waiting for the first completed scan"
			if snapshot.Stale {
				reason = "not enough fresh closed-candle data; waiting for the next completed scan"
			}
		}
		return nil, fmt.Errorf("%s local analysis is not ready: %s", exchange, reason)
	}
	out := []store.LocalSignal{}
	for _, r := range snapshot.Results {
		if r.Exchange == exchange && r.Confirmed && r.Direction != "wait" && r.Score >= minScore && Fresh(r, s.now()) {
			out = append(out, r)
		}
	}
	if limit < 1 || limit > store.MaxCandidateCoins {
		limit = store.MaxCandidateCoins
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Service) ValidateOpen(exchange, symbol, action string, minScore float64, limit int) error {
	results, err := s.Candidates(exchange, minScore, limit)
	if err != nil {
		return err
	}
	want := "long"
	if action == "open_short" {
		want = "short"
	} else if action != "open_long" {
		return fmt.Errorf("not an opening action")
	}
	for _, result := range results {
		if result.Symbol == symbol && result.Direction == want {
			return nil
		}
	}
	return fmt.Errorf("%s %s is not approved by current %s local analysis", symbol, action, exchange)
}

func (s *Service) Scan(parent context.Context, exchange string) error {
	if !Supported(exchange) {
		return fmt.Errorf("unsupported signal exchange %q", exchange)
	}
	s.mu.Lock()
	if s.running[exchange] {
		s.mu.Unlock()
		return fmt.Errorf("scan already running")
	}
	s.running[exchange] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running[exchange] = false
		s.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	scan := &store.LocalSignalScan{Exchange: exchange, ObservedAt: time.Now().UnixMilli(), Results: []store.LocalSignal{}, Failures: []string{}}
	provider, err := s.provider(exchange)
	if err == nil && provider.Exchange() != exchange {
		err = fmt.Errorf("market provider identity mismatch")
	}
	var symbols []string
	if err == nil {
		symbols, err = provider.ListPerpetualSymbols(ScanLimit)
	}
	if len(symbols) > ScanLimit {
		symbols = symbols[:ScanLimit]
	}
	if err != nil || len(symbols) == 0 {
		scan.Error = "Market list unavailable"
	} else {
		seen := make(map[string]bool)
		unique := []string{}
		for _, symbol := range symbols {
			if !seen[symbol] {
				seen[symbol] = true
				unique = append(unique, symbol)
			}
		}
		scan.Expected = len(unique)
		var wg sync.WaitGroup
		var resultMu sync.Mutex
	scanSymbols:
		for _, symbol := range unique {
			select {
			case <-ctx.Done():
				break scanSymbols
			case s.slots <- struct{}{}:
			}
			if ctx.Err() != nil {
				<-s.slots
				break
			}
			wg.Add(1)
			go func(symbol string) {
				defer wg.Done()
				defer func() { <-s.slots }()
				result, fetchErr := collect(ctx, provider, symbol)
				resultMu.Lock()
				defer resultMu.Unlock()
				if fetchErr != nil {
					scan.Failures = append(scan.Failures, symbol+": "+shortError(fetchErr))
					return
				}
				scan.Results = append(scan.Results, result)
			}(symbol)
		}
		wg.Wait()
		scan.Succeeded = len(scan.Results)
		if scan.Succeeded*5 < scan.Expected*4 {
			scan.Error = "Fewer than 80% of markets have complete current candles"
		}
		if ctx.Err() != nil {
			scan.Error = "Market analysis timed out or stopped"
		}
	}
	sort.Slice(scan.Results, func(i, j int) bool {
		if scan.Results[i].Score == scan.Results[j].Score {
			return scan.Results[i].Symbol < scan.Results[j].Symbol
		}
		return scan.Results[i].Score > scan.Results[j].Score
	})
	sort.Strings(scan.Failures)
	persistErr := s.repo.Save(scan)
	if persistErr != nil {
		scan.Error = "Could not save market analysis"
	}
	s.mu.Lock()
	s.latest[exchange] = scan
	s.ready[exchange] = scan.Error == ""
	s.mu.Unlock()
	if persistErr != nil {
		return fmt.Errorf("persist scan: %w", persistErr)
	}
	if err := s.repo.Prune(time.Now()); err != nil {
		logger.Warnf("Local analysis history cleanup: %v", err)
	}
	if scan.Error != "" {
		return fmt.Errorf("%s", scan.Error)
	}
	return nil
}

func shortError(err error) string {
	s := strings.ReplaceAll(err.Error(), "\n", " ")
	if len(s) > 180 {
		s = s[:180]
	}
	return s
}

type contextualKlines interface {
	GetKlinesFreshContext(context.Context, string, string, int) ([]market.Kline, error)
}

func collect(ctx context.Context, p market.MarketDataProvider, symbol string) (store.LocalSignal, error) {
	reader, ok := p.(contextualKlines)
	if !ok {
		return store.LocalSignal{}, fmt.Errorf("cancellable market data is unavailable")
	}
	data := make(map[string][]market.Kline)
	for _, tf := range Timeframes {
		if err := ctx.Err(); err != nil {
			return store.LocalSignal{}, err
		}
		bars, err := reader.GetKlinesFreshContext(ctx, symbol, tf, 182)
		if err != nil {
			return store.LocalSignal{}, fmt.Errorf("%s candles unavailable: %w", tf, err)
		}
		data[tf] = bars
	}
	result, err := Analyze(p.Exchange(), symbol, data, time.Now())
	if err != nil {
		return result, err
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if p.Capabilities().Has(market.CapabilityFunding) {
		funding, ferr := p.GetFundingSnapshot(symbol)
		if ferr == nil && funding != nil && finite(funding.Rate) && funding.Time > 0 && time.Since(time.UnixMilli(funding.Time)) >= 0 && time.Since(time.UnixMilli(funding.Time)) < 10*time.Minute {
			rate := funding.Rate
			result.FundingRate = &rate
			result.FundingTime = funding.Time
		} else {
			result.Warnings = append(result.Warnings, "funding_unavailable")
		}
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if p.Capabilities().Has(market.CapabilityOpenInterest) {
		oi, oerr := p.GetOpenInterest(symbol)
		if oerr == nil && oi != nil && finite(oi.Latest) && oi.Latest > 0 && oi.Unit != "" {
			value := oi.Latest
			result.OpenInterest = &value
			result.OpenInterestUnit = oi.Unit
			result.OpenInterestObservedAt = time.Now().UnixMilli()
		} else {
			result.Warnings = append(result.Warnings, "open_interest_unavailable")
		}
	}
	return result, ctx.Err()
}
